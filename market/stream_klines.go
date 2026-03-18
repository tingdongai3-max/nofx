package market

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"nofx/binanceguard"
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

	okxWSURL            = "wss://ws.okx.com:8443/ws/v5/public"
	okxArgsPerConn      = 200   // OKX 单次订阅 args 数量限制
	okxPingInterval     = 20 * time.Second
	okxReadDeadline     = 2 * time.Minute
	okxRESTBaseURL      = "https://www.okx.com"
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

type klinePrewarmState struct {
	done chan struct{}
	err  error
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
	klinePrewarms        = make(map[klineSeriesKey]*klinePrewarmState)
	binanceMultiplexDone chan struct{}
	binanceMultiplexOnce sync.Once
	okxMultiplexDone     chan struct{}
	okxMultiplexOnce     sync.Once
)

// symbolToOkxInstId 将 BTCUSDT 转为 OKX 合约 instId：BTC-USDT-SWAP
func symbolToOkxInstId(symbol string) string {
	symbol = Normalize(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		base := strings.TrimSuffix(symbol, "USDT")
		return base + "-USDT-SWAP"
	}
	return symbol + "-SWAP"
}

// intervalToOkxChannel 将 5m/1h 转为 OKX channel：candle5m, candle1H
func intervalToOkxChannel(interval string) string {
	interval = strings.TrimSpace(strings.ToLower(interval))
	switch interval {
	case "1m":
		return "candle1m"
	case "3m":
		return "candle3m"
	case "5m":
		return "candle5m"
	case "15m":
		return "candle15m"
	case "30m":
		return "candle30m"
	case "1h":
		return "candle1H"
	case "2h":
		return "candle2H"
	case "4h":
		return "candle4H"
	case "6h":
		return "candle6H"
	case "8h":
		return "candle8H"
	case "12h":
		return "candle12H"
	case "1d":
		return "candle1D"
	case "3d":
		return "candle3D"
	case "1w":
		return "candle1W"
	default:
		return "candle5m"
	}
}

// intervalToOkxBar REST 参数 bar：1m, 5m, 1H, 1D 等
func intervalToOkxBar(interval string) string {
	interval = strings.TrimSpace(strings.ToLower(interval))
	switch interval {
	case "1h":
		return "1H"
	case "2h":
		return "2H"
	case "4h":
		return "4H"
	case "6h":
		return "6H"
	case "8h":
		return "8H"
	case "12h":
		return "12H"
	case "1d":
		return "1D"
	case "3d":
		return "3D"
	case "1w":
		return "1W"
	default:
		return interval
	}
}

// fetchOkxKlinesREST 使用 OKX 官方 REST 拉取 K 线预热（无需鉴权）
func fetchOkxKlinesREST(symbol, interval string, limit int) ([]Kline, error) {
	instId := symbolToOkxInstId(symbol)
	bar := intervalToOkxBar(interval)
	url := okxRESTBaseURL + "/api/v5/market/candles?instId=" + instId + "&bar=" + bar + "&limit=" + strconv.Itoa(limit)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var okxResp struct {
		Code string        `json:"code"`
		Data []interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &okxResp); err != nil {
		return nil, err
	}
	if okxResp.Code != "0" || len(okxResp.Data) == 0 {
		return nil, nil
	}
	// OKX 返回 [ts, o, h, l, c, vol, volCcy, volCcyQuote, confirm]，按时间升序；我们需倒序为 oldest-first
	klines := make([]Kline, 0, len(okxResp.Data))
	for i := len(okxResp.Data) - 1; i >= 0; i-- {
		arr, _ := okxResp.Data[i].([]interface{})
		if len(arr) < 6 {
			continue
		}
		tsMs, _ := strconv.ParseInt(fmtStr(arr[0]), 10, 64)
		o, _ := strconv.ParseFloat(fmtStr(arr[1]), 64)
		h, _ := strconv.ParseFloat(fmtStr(arr[2]), 64)
		l, _ := strconv.ParseFloat(fmtStr(arr[3]), 64)
		c, _ := strconv.ParseFloat(fmtStr(arr[4]), 64)
		vol, _ := strconv.ParseFloat(fmtStr(arr[5]), 64)
		barMs := intervalToBarMs(interval)
		klines = append(klines, Kline{
			OpenTime:  tsMs,
			CloseTime: tsMs + barMs - 1,
			Open:      o,
			High:      h,
			Low:       l,
			Close:     c,
			Volume:    vol,
		})
	}
	return klines, nil
}

func fmtStr(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

func intervalToBarMs(interval string) int64 {
	interval = strings.TrimSpace(strings.ToLower(interval))
	switch interval {
	case "1m":
		return 60 * 1000
	case "3m":
		return 3 * 60 * 1000
	case "5m":
		return 5 * 60 * 1000
	case "15m":
		return 15 * 60 * 1000
	case "30m":
		return 30 * 60 * 1000
	case "1h":
		return 3600 * 1000
	case "2h":
		return 2 * 3600 * 1000
	case "4h":
		return 4 * 3600 * 1000
	case "6h":
		return 6 * 3600 * 1000
	case "8h":
		return 8 * 3600 * 1000
	case "12h":
		return 12 * 3600 * 1000
	case "1d":
		return 24 * 3600 * 1000
	case "3d":
		return 3 * 24 * 3600 * 1000
	case "1w":
		return 7 * 24 * 3600 * 1000
	default:
		return 5 * 60 * 1000
	}
}

// ensureKlineStream 为 (symbol, interval, exchange) 注册 ring，并将首轮 REST 预热单例化。
func ensureKlineStream(symbol, interval, exchange string) error {
	symbol = Normalize(symbol)
	exchangeEnum := mapExchangeToEnum(exchange)
	intervalEnum, ok := mapIntervalToEnum(interval)
	if !ok {
		return nil
	}

	if IsXyzDexAsset(symbol) {
		return nil
	}

	key := klineSeriesKey{
		Symbol:   symbol,
		Exchange: exchangeEnum,
		Interval: intervalEnum,
	}

	klineStreamsMu.Lock()
	ring, exists := klineStreams[key]
	if !exists {
		ring = newKlineRing(2000)
		klineStreams[key] = ring
	}
	if exists && ring != nil && len(ring.snapshot(1)) > 0 {
		klineStreamsMu.Unlock()
		return nil
	}
	prewarm, prewarmExists := klinePrewarms[key]
	if !prewarmExists {
		prewarm = &klinePrewarmState{done: make(chan struct{})}
		klinePrewarms[key] = prewarm
	}
	klineStreamsMu.Unlock()

	if prewarmExists {
		<-prewarm.done
		return prewarm.err
	}

	// REST 预热
	const restPreFetchBars = 200
	var prewarmErr error
	if exchangeEnum == coinank_enum.Binance {
		if err := binanceguard.CheckCircuitBreaker(); err != nil {
			prewarmErr = err
		} else {
			api := GetAPIClient()
			klines, err := api.GetKlines(symbol, interval, restPreFetchBars)
			if err == nil && len(klines) > 0 {
				ring.loadHistory(klines)
				logger.Infof("✓ K-line REST pre-warm (Binance): %s %s, %d bars", symbol, interval, len(klines))
			} else if err != nil {
				prewarmErr = err
				logger.Warnf("⚠️ Binance K-line pre-warm failed (%s %s): %v", symbol, interval, err)
			}
		}
	} else if exchangeEnum == coinank_enum.Okex {
		klines, err := fetchOkxKlinesREST(symbol, interval, restPreFetchBars)
		if err == nil && len(klines) > 0 {
			ring.loadHistory(klines)
			logger.Infof("✓ K-line REST pre-warm (OKX): %s %s, %d bars", symbol, interval, len(klines))
		} else if err != nil {
			prewarmErr = err
			logger.Warnf("⚠️ OKX K-line pre-warm failed (%s %s): %v", symbol, interval, err)
		}
		startOkxMultiplex()
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
			prewarmErr = err
			logger.Warnf("⚠️ K-line REST pre-warm failed (%s %s %s): %v", symbol, exchange, interval, err)
		}
	}

	klineStreamsMu.Lock()
	prewarm.err = prewarmErr
	close(prewarm.done)
	delete(klinePrewarms, key)
	klineStreamsMu.Unlock()

	if exchangeEnum == coinank_enum.Binance {
		startBinanceMultiplex()
	}
	return prewarmErr
}

// startOkxMultiplex 启动 OKX 单连接多路复用（按需只起一次）
func startOkxMultiplex() {
	okxMultiplexOnce.Do(func() {
		okxMultiplexDone = make(chan struct{})
		go runOkxMultiplex(okxMultiplexDone)
	})
}

// runOkxMultiplex 多路复用：将所有 OKX 流按 okxArgsPerConn 个一组拆成多连接
func runOkxMultiplex(done chan struct{}) {
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

		keys := getKeysForExchange(coinank_enum.Okex)
		if len(keys) == 0 {
			return
		}

		var args []okxArg
		for _, k := range keys {
			args = append(args, okxArg{
				Channel: intervalToOkxChannel(string(k.Interval)),
				InstID:  symbolToOkxInstId(k.Symbol),
			})
		}

		chunks := chunkOkxArgs(args, okxArgsPerConn)
		var wg sync.WaitGroup
		for i, chunk := range chunks {
			wg.Add(1)
			go func(chunkIndex int, a []okxArg) {
				defer wg.Done()
				runOkxConn(chunkIndex, a, done)
			}(i+1, chunk)
		}
		wg.Wait()

		logger.Infof("✓ OKX kline multiplex reconnecting, REST sync for %d streams", len(keys))
		for _, key := range keys {
			klineStreamsMu.Lock()
			ring := klineStreams[key]
			klineStreamsMu.Unlock()
			if ring == nil {
				continue
			}
			klines, err := fetchOkxKlinesREST(key.Symbol, string(key.Interval), 200)
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

func chunkOkxArgs(args []okxArg, size int) [][]okxArg {
	var out [][]okxArg
	for i := 0; i < len(args); i += size {
		end := i + size
		if end > len(args) {
			end = len(args)
		}
		out = append(out, args[i:end])
	}
	return out
}

// okxArg OKX 订阅参数
type okxArg struct {
	Channel string `json:"channel"`
	InstID  string `json:"instId"`
}

// runOkxConn 单连接：发送 subscribe，保活 ping 20s，解析 data 写 ring
func runOkxConn(chunkIndex int, args []okxArg, done chan struct{}) {
	if len(args) == 0 {
		return
	}

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.Dial(okxWSURL, nil)
	if err != nil {
		logger.Warnf("⚠️ OKX kline WS dial failed: %v", err)
		return
	}
	defer conn.Close()

	subscribePayload := map[string]interface{}{
		"op":   "subscribe",
		"args": args,
	}
	subBody, _ := json.Marshal(subscribePayload)
	if err := conn.WriteMessage(websocket.TextMessage, subBody); err != nil {
		logger.Warnf("⚠️ OKX subscribe write failed: %v", err)
		return
	}

	conn.SetReadDeadline(time.Now().Add(okxReadDeadline))
	logger.Infof("[WS] Connected OKX multiplex stream chunk #%d with %d streams", chunkIndex, len(args))

	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(okxPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-stopPing:
				return
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
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
			logger.Warnf("[WARN] OKX WS connection dropped, attempting reconnect... error: %v", err)
			return
		}

		conn.SetReadDeadline(time.Now().Add(okxReadDeadline))

		// 兼容 "pong" 文本响应
		if len(msg) == 4 && string(msg) == "pong" {
			continue
		}

		var okxMsg struct {
			Arg  *struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			} `json:"arg"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(msg, &okxMsg); err != nil || okxMsg.Arg == nil {
			continue
		}

		channel := okxMsg.Arg.Channel
		instId := okxMsg.Arg.InstID
		if !strings.HasPrefix(channel, "candle") {
			continue
		}
		// instId: BTC-USDT-SWAP -> symbol BTCUSDT
		symbol := strings.ReplaceAll(instId, "-USDT-SWAP", "USDT")
		symbol = strings.ReplaceAll(symbol, "-", "")
		symbol = strings.ToUpper(symbol)
		// channel: candle5m -> interval 5m
		intervalStr := strings.TrimPrefix(channel, "candle")
		if len(intervalStr) > 0 && intervalStr[len(intervalStr)-1] == 'H' {
			intervalStr = strings.TrimSuffix(intervalStr, "H") + "h"
		} else if len(intervalStr) > 0 && intervalStr[len(intervalStr)-1] == 'D' {
			intervalStr = strings.TrimSuffix(intervalStr, "D") + "d"
		} else if len(intervalStr) > 0 && intervalStr[len(intervalStr)-1] == 'W' {
			intervalStr = strings.TrimSuffix(intervalStr, "W") + "w"
		}

		var dataArr []interface{}
		if err := json.Unmarshal(okxMsg.Data, &dataArr); err != nil || len(dataArr) == 0 {
			continue
		}

		// 取最后一根（最新）[ts, o, h, l, c, vol, ...]
		last := dataArr[len(dataArr)-1]
		arr, _ := last.([]interface{})
		if len(arr) < 6 {
			continue
		}
		tsMs, _ := strconv.ParseInt(fmtStr(arr[0]), 10, 64)
		o, _ := strconv.ParseFloat(fmtStr(arr[1]), 64)
		h, _ := strconv.ParseFloat(fmtStr(arr[2]), 64)
		l, _ := strconv.ParseFloat(fmtStr(arr[3]), 64)
		c, _ := strconv.ParseFloat(fmtStr(arr[4]), 64)
		vol, _ := strconv.ParseFloat(fmtStr(arr[5]), 64)
		barMs := intervalToBarMs(intervalStr)
		kline := Kline{
			OpenTime:  tsMs,
			CloseTime: tsMs + barMs - 1,
			Open:      o,
			High:      h,
			Low:       l,
			Close:     c,
			Volume:    vol,
		}

		intervalEnum, ok := mapIntervalToEnum(intervalStr)
		if !ok {
			continue
		}
		key := klineSeriesKey{
			Symbol:   symbol,
			Exchange: coinank_enum.Okex,
			Interval: intervalEnum,
		}
		klineStreamsMu.Lock()
		ring := klineStreams[key]
		klineStreamsMu.Unlock()
		if ring != nil {
			ring.append(kline)
		}
		// 无锁热槽：仅内存覆盖，不等待 DB
		SetLatestPrice(symbol, "okx", c, kline.CloseTime)

		if rand.Intn(1000) == 0 {
			logger.Infof("[WS Pulse] OKX 收到实时行情: %s %s, 当前价: %s", channel, instId, fmtStr(arr[4]))
		}

		select {
		case klineUpdateCh <- KlineUpdateEvent{Symbol: symbol, Exchange: "okx", Interval: intervalStr}:
		default:
		}
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
		// 无锁热槽：仅内存覆盖，不等待 DB，满足 ~1ms 内完成
		SetLatestPrice(symbol, "binance", closeP, k.CloseTime)

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

// GetRealtimeKlines returns a snapshot of the in-memory kline ring for the stream, if available.
func GetRealtimeKlines(symbol, interval, exchange string, count int) ([]Kline, bool) {
	return getRealtimeKlines(symbol, interval, exchange, count)
}

func ReleaseRealtimeKlineStream(symbol, interval, exchange string) {
	symbol = Normalize(symbol)
	exchangeEnum := mapExchangeToEnum(exchange)
	intervalEnum, ok := mapIntervalToEnum(interval)
	if !ok {
		return
	}
	key := klineSeriesKey{
		Symbol:   symbol,
		Exchange: exchangeEnum,
		Interval: intervalEnum,
	}
	klineStreamsMu.Lock()
	delete(klineStreams, key)
	klineStreamsMu.Unlock()
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
