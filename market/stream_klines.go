package market

import (
	"context"
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"nofx/logger"
	"nofx/provider/coinank"
	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
)

const (
	binanceFuturesStreamURL = "wss://fstream.binance.com/stream"
	binanceStreamsPerConn   = 100   // 单连接约 200–1024，取 100 稳妥
	wsPingInterval          = 3 * time.Minute
	wsReadDeadline          = 10 * time.Minute
)

// klineSeriesKey 唯一标识一条 K 线流 (symbol + exchange + interval)
type klineSeriesKey struct {
	Symbol   string
	Exchange coinank_enum.Exchange
	Interval coinank_enum.Interval
}

// KlineUpdateEvent 表示某条 K 线序列有推送更新（数据流驱动，非轮询）
type KlineUpdateEvent struct {
	Symbol   string
	Exchange string
	Interval string
}

var (
	klineUpdateCh = make(chan KlineUpdateEvent, 256)
)

func SubscribeKlineUpdates() <-chan KlineUpdateEvent {
	return klineUpdateCh
}

type klineRing struct {
	mu   sync.RWMutex
	data []Kline
	cap  int
}

func newKlineRing(capacity int) *klineRing {
	return &klineRing{
		data: make([]Kline, 0, capacity),
		cap:  capacity,
	}
}

func (r *klineRing) append(k Kline) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(r.data)
	if n > 0 {
		last := &r.data[n-1]
		if last.OpenTime == k.OpenTime {
			*last = k
			return
		}
		if k.OpenTime < last.OpenTime {
			return
		}
	}

	if n < r.cap {
		r.data = append(r.data, k)
		return
	}
	copy(r.data, r.data[1:])
	r.data[r.cap-1] = k
}

func (r *klineRing) loadHistory(klines []Kline) {
	if len(klines) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(klines) <= r.cap {
		r.data = make([]Kline, len(klines))
		copy(r.data, klines)
		return
	}
	r.data = make([]Kline, r.cap)
	copy(r.data, klines[len(klines)-r.cap:])
}

func (r *klineRing) snapshot(count int) []Kline {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n := len(r.data)
	if n == 0 {
		return nil
	}
	if count <= 0 || count >= n {
		cp := make([]Kline, n)
		copy(cp, r.data)
		return cp
	}
	cp := make([]Kline, count)
	copy(cp, r.data[n-count:])
	return cp
}

var (
	klineStreamsMu       sync.Mutex
	klineStreams         = make(map[klineSeriesKey]*klineRing)
	binanceMultiplexDone chan struct{}
	binanceMultiplexOnce sync.Once
)

// ensureKlineStream 为 (symbol, interval, exchange) 注册 ring 并触发多路复用（仅 Binance 走 WS）
func ensureKlineStream(symbol, interval, exchange string) {
	symbol = Normalize(symbol)
	exchangeEnum := mapExchangeToEnum(exchange)
	intervalEnum, ok := mapIntervalToEnum(interval)
	if !ok {
		return
	}

	if IsXyzDexAsset(symbol) {
		return
	}

	key := klineSeriesKey{
		Symbol:   symbol,
		Exchange: exchangeEnum,
		Interval: intervalEnum,
	}

	klineStreamsMu.Lock()
	if _, exists := klineStreams[key]; exists {
		klineStreamsMu.Unlock()
		return
	}
	ring := newKlineRing(2000)
	klineStreams[key] = ring
	klineStreamsMu.Unlock()

	// REST 预热
	const restPreFetchBars = 200
	if exchangeEnum == coinank_enum.Binance {
		api := GetAPIClient()
		klines, err := api.GetKlines(symbol, interval, restPreFetchBars)
		if err == nil && len(klines) > 0 {
			ring.loadHistory(klines)
			logger.Infof("✓ K-line REST pre-warm (Binance): %s %s, %d bars", symbol, interval, len(klines))
		} else if err != nil {
			logger.Warnf("⚠️ Binance K-line pre-warm failed (%s %s): %v", symbol, interval, err)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ts := time.Now().UTC().UnixMilli()
		coinankKlines, err := coinank_api.Kline(ctx, symbol, exchangeEnum, ts, coinank_enum.To, restPreFetchBars, intervalEnum)
		cancel()
		if err == nil && len(coinankKlines) > 0 {
			klines := make([]Kline, len(coinankKlines))
			for i, k := range coinankKlines {
				klines[i] = coinankResultToKline(k)
			}
			ring.loadHistory(klines)
			logger.Infof("✓ K-line REST pre-warm: %s %s %s, %d bars", symbol, exchange, interval, len(klines))
		} else if err != nil {
			logger.Warnf("⚠️ K-line REST pre-warm failed (%s %s %s): %v", symbol, exchange, interval, err)
		}
	}

	if exchangeEnum == coinank_enum.Binance {
		startBinanceMultiplex()
	}
}

// startBinanceMultiplex 启动 Binance 单连接多路复用（按需只起一次）
func startBinanceMultiplex() {
	binanceMultiplexOnce.Do(func() {
		binanceMultiplexDone = make(chan struct{})
		go runBinanceMultiplex(binanceMultiplexDone)
	})
}

// runBinanceMultiplex 多路复用：将所有 Binance 流按 100 个一组拆成多连接，每连接 URL 聚合订阅
func runBinanceMultiplex(done chan struct{}) {
	defer close(done)

	const (
		initialBackoff = 1 * time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff

	for {
		select {
		case <-done:
			return
		default:
		}

		keys := getKeysForExchange(coinank_enum.Binance)
		if len(keys) == 0 {
			return
		}

		// 每 binanceStreamsPerConn 个流一个连接
		streamNames := make([]string, 0, len(keys))
		for _, k := range keys {
			streamNames = append(streamNames, strings.ToLower(k.Symbol)+"@kline_"+string(k.Interval))
		}

		chunks := chunkStrings(streamNames, binanceStreamsPerConn)
		var wg sync.WaitGroup
		for i, chunk := range chunks {
			wg.Add(1)
			go func(chunkIndex int, streams []string) {
				defer wg.Done()
				runBinanceConn(chunkIndex, streams, done)
			}(i+1, chunk)
		}
		wg.Wait()

		// 全部断开后重连前 REST 全量同步
		logger.Infof("✓ Binance kline multiplex reconnecting, REST sync for %d streams", len(keys))
		for _, key := range keys {
			klineStreamsMu.Lock()
			ring := klineStreams[key]
			klineStreamsMu.Unlock()
			if ring == nil {
				continue
			}
			api := GetAPIClient()
			klines, err := api.GetKlines(key.Symbol, string(key.Interval), 200)
			if err == nil && len(klines) > 0 {
				ring.loadHistory(klines)
			}
		}

		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func chunkStrings(s []string, size int) [][]string {
	var out [][]string
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[i:end])
	}
	return out
}

// binanceCombinedMessage 组合流单条消息：{"stream":"btcusdt@kline_5m","data":{...}}
type binanceCombinedMessage struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

// binanceKlineEvent 对应 data 里的 kline 事件，k 为 K 线内容
type binanceKlineEvent struct {
	EventType string         `json:"e"`
	Symbol    string         `json:"s"`
	Kline     binanceKlineK  `json:"k"`
}

type binanceKlineK struct {
	OpenTime  int64  `json:"t"`
	CloseTime int64  `json:"T"`
	Interval  string `json:"i"`
	Open      string `json:"o"`
	Close     string `json:"c"`
	High      string `json:"h"`
	Low       string `json:"l"`
	Volume    string `json:"v"`
}

func runBinanceConn(chunkIndex int, streamNames []string, done chan struct{}) {
	if len(streamNames) == 0 {
		return
	}

	streamsParam := strings.Join(streamNames, "/")
	url := binanceFuturesStreamURL + "?streams=" + streamsParam

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		logger.Warnf("⚠️ Binance kline WS dial failed: %v", err)
		return
	}
	defer conn.Close()

	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})
	conn.SetPongHandler(func(appData string) error {
		return nil
	})

	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	logger.Infof("[WS] Connected Binance multiplex stream chunk #%d with %d streams", chunkIndex, len(streamNames))

	// 保活：每 3 分钟发 Ping
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-stopPing:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			logger.Warnf("[WARN] Binance WS connection dropped, attempting reconnect... error: %v", err)
			return
		}

		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))

		var wrapped binanceCombinedMessage
		if err := json.Unmarshal(msg, &wrapped); err != nil {
			continue
		}

		// stream 格式: btcusdt@kline_5m
		parts := strings.SplitN(wrapped.Stream, "@", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "kline_") {
			continue
		}
		symbolLower := parts[0]
		intervalStr := strings.TrimPrefix(parts[1], "kline_")
		symbol := strings.ToUpper(symbolLower)

		var ev binanceKlineEvent
		if err := json.Unmarshal(wrapped.Data, &ev); err != nil {
			continue
		}
		k := ev.Kline

		open, _ := strconv.ParseFloat(k.Open, 64)
		high, _ := strconv.ParseFloat(k.High, 64)
		low, _ := strconv.ParseFloat(k.Low, 64)
		closeP, _ := strconv.ParseFloat(k.Close, 64)
		vol, _ := strconv.ParseFloat(k.Volume, 64)

		intervalEnum, ok := mapIntervalToEnum(intervalStr)
		if !ok {
			continue
		}
		key := klineSeriesKey{
			Symbol:   symbol,
			Exchange: coinank_enum.Binance,
			Interval: intervalEnum,
		}
		klineStreamsMu.Lock()
		ring := klineStreams[key]
		klineStreamsMu.Unlock()
		if ring == nil {
			continue
		}

		ring.append(Kline{
			OpenTime:  k.OpenTime,
			CloseTime: k.CloseTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closeP,
			Volume:    vol,
		})

		// 极低频采样日志：确认数据流脉搏，避免刷屏
		if rand.Intn(1000) == 0 {
			logger.Infof("[WS Pulse] 收到实时行情: %s, 当前价: %s", wrapped.Stream, k.Close)
		}

		select {
		case klineUpdateCh <- KlineUpdateEvent{Symbol: symbol, Exchange: "binance", Interval: intervalStr}:
		default:
		}
	}
}

func getKeysForExchange(exchangeEnum coinank_enum.Exchange) []klineSeriesKey {
	klineStreamsMu.Lock()
	defer klineStreamsMu.Unlock()
	var keys []klineSeriesKey
	for k := range klineStreams {
		if k.Exchange == exchangeEnum {
			keys = append(keys, k)
		}
	}
	return keys
}

func refillKlineRing(symbol, interval, exchange string, klines []Kline) {
	if len(klines) == 0 {
		return
	}
	symbol = Normalize(symbol)
	exchangeEnum := mapExchangeToEnum(exchange)
	intervalEnum, ok := mapIntervalToEnum(interval)
	if !ok {
		return
	}
	if IsXyzDexAsset(symbol) {
		return
	}
	key := klineSeriesKey{Symbol: symbol, Exchange: exchangeEnum, Interval: intervalEnum}
	klineStreamsMu.Lock()
	ring := klineStreams[key]
	klineStreamsMu.Unlock()
	if ring != nil {
		ring.loadHistory(klines)
		logger.Infof("✓ K-line cache refilled: %s %s %s, %d bars (stale data was replaced)", symbol, exchange, interval, len(klines))
	}
}

func getRealtimeKlines(symbol, interval, exchange string, count int) ([]Kline, bool) {
	symbol = Normalize(symbol)
	exchangeEnum := mapExchangeToEnum(exchange)
	intervalEnum, ok := mapIntervalToEnum(interval)
	if !ok {
		return nil, false
	}
	if IsXyzDexAsset(symbol) {
		return nil, false
	}

	key := klineSeriesKey{
		Symbol:   symbol,
		Exchange: exchangeEnum,
		Interval: intervalEnum,
	}

	klineStreamsMu.Lock()
	ring, exists := klineStreams[key]
	klineStreamsMu.Unlock()
	if !exists || ring == nil {
		return nil, false
	}
	data := ring.snapshot(count)
	if len(data) == 0 {
		return nil, false
	}
	return data, true
}

func coinankResultToKline(k coinank.KlineResult) Kline {
	return Kline{
		OpenTime:  k.StartTime,
		Open:      k.Open,
		High:      k.High,
		Low:       k.Low,
		Close:     k.Close,
		Volume:    k.Volume,
		CloseTime: k.EndTime,
	}
}

func mapExchangeToEnum(exchange string) coinank_enum.Exchange {
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "binance":
		return coinank_enum.Binance
	case "bybit":
		return coinank_enum.Bybit
	case "okx", "okex":
		return coinank_enum.Okex
	case "bitget":
		return coinank_enum.Bitget
	case "gate":
		return coinank_enum.Gate
	case "hyperliquid":
		return coinank_enum.Hyperliquid
	case "aster":
		return coinank_enum.Aster
	default:
		return coinank_enum.Binance
	}
}

// mapIntervalToEnum 支持币安周期字符串 (1m, 3m, 5m, 15m, 30m, 1h, 2h, 4h, 6h, 8h, 12h, 1d, 3d, 1w)
func mapIntervalToEnum(interval string) (coinank_enum.Interval, bool) {
	interval = strings.TrimSpace(strings.ToLower(interval))
	switch interval {
	case "1m":
		return coinank_enum.Minute1, true
	case "3m":
		return coinank_enum.Minute3, true
	case "5m":
		return coinank_enum.Minute5, true
	case "15m":
		return coinank_enum.Minute15, true
	case "30m":
		return coinank_enum.Minute30, true
	case "1h":
		return coinank_enum.Hour1, true
	case "2h":
		return coinank_enum.Hour2, true
	case "4h":
		return coinank_enum.Hour4, true
	case "6h":
		return coinank_enum.Hour6, true
	case "8h":
		return coinank_enum.Hour8, true
	case "12h":
		return coinank_enum.Hour12, true
	case "1d":
		return coinank_enum.Day1, true
	case "3d":
		return coinank_enum.Day3, true
	case "1w":
		return coinank_enum.Week1, true
	default:
		return "", false
	}
}

