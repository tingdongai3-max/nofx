package market

import (
	"context"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/provider/coinank"
	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
)

// klineSeriesKey identifies a unique kline stream (symbol + exchange + interval).
type klineSeriesKey struct {
	Symbol   string
	Exchange coinank_enum.Exchange
	Interval coinank_enum.Interval
}

// KlineUpdateEvent 表示某条 K 线序列有推送更新（数据流驱动，非轮询）
type KlineUpdateEvent struct {
	Symbol   string // 如 POWERUSDT
	Exchange string // 如 binance, okx
	Interval string // 如 5m, 15m
}

var (
	klineUpdateCh = make(chan KlineUpdateEvent, 256) // 数据流推送时写入，供 ATR 机器狗等消费
)

// SubscribeKlineUpdates 返回 K 线更新流；交易所推送新数据时写入，避免轮询与封禁
func SubscribeKlineUpdates() <-chan KlineUpdateEvent {
	return klineUpdateCh
}

// klineRing is a simple in-memory ring buffer for Kline data.
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
		// Same bar (same open time) → update in-place to reflect live tick.
		if last.OpenTime == k.OpenTime {
			*last = k
			return
		}
		// Out-of-order old data: ignore.
		if k.OpenTime < last.OpenTime {
			return
		}
	}

	if n < r.cap {
		r.data = append(r.data, k)
		return
	}

	// When full, drop oldest (simple sliding window).
	copy(r.data, r.data[1:])
	r.data[r.cap-1] = k
}

// loadHistory bulk-loads historical K-lines (e.g. from REST) as the ring base.
// klines should be oldest-first; only the last r.cap bars are kept.
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
	klineMultiplexActive = make(map[coinank_enum.Exchange]chan struct{}) // exchange -> done chan
	klineMultiplexMu     sync.Mutex
)

// ensureKlineStream ensures there is a running WebSocket stream for the given symbol/exchange/interval.
// 多路复用：同一 exchange 共用一个 WS 连接，批量订阅，避免 120 个独立连接。
func ensureKlineStream(symbol, interval, exchange string) {
	// Normalize inputs for key.
	symbol = Normalize(symbol)
	exchangeEnum := mapExchangeToEnum(exchange)
	intervalEnum, ok := mapIntervalToEnum(interval)
	if !ok {
		return
	}

	// xyz assets are handled via Hyperliquid HTTP API and are not part of CoinAnk kline WS.
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

	// REST 预热：在启动 WS 前先拉取 200 根历史 K 线塞入 ring，避免冷启动只有 1 根
	const restPreFetchBars = 200
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	ts := time.Now().UnixMilli()
	coinankKlines, err := coinank_api.Kline(ctx, key.Symbol, key.Exchange, ts, coinank_enum.To, restPreFetchBars, key.Interval)
	cancel()
	if err == nil && len(coinankKlines) > 0 {
		klines := make([]Kline, len(coinankKlines))
		for i, k := range coinankKlines {
			klines[i] = coinankResultToKline(k)
		}
		ring.loadHistory(klines)
		logger.Infof("✓ K-line REST pre-warm: %s %s %s, %d bars", key.Symbol, key.Exchange, key.Interval, len(klines))
	} else if err != nil {
		logger.Warnf("⚠️ K-line REST pre-warm failed (%s %s %s): %v, WS will fill from live only", key.Symbol, key.Exchange, key.Interval, err)
	}

	startKlineMultiplex(key.Exchange, exchange)
}

// startKlineMultiplex 确保该 exchange 的多路复用连接已启动（单连接批量订阅）
func startKlineMultiplex(exchangeEnum coinank_enum.Exchange, exchangeStr string) {
	klineMultiplexMu.Lock()
	if _, running := klineMultiplexActive[exchangeEnum]; running {
		klineMultiplexMu.Unlock()
		return
	}
	done := make(chan struct{})
	klineMultiplexActive[exchangeEnum] = done
	klineMultiplexMu.Unlock()
	go runKlineMultiplex(exchangeEnum, exchangeStr, done)
}

// runKlineMultiplex 单连接多路复用：同一 exchange 下所有 (symbol,interval) 共用一个 WS，批量订阅
// 断线重连时 REST 全量同步，避免 ScreenerCache/Kline 脏数据
func runKlineMultiplex(exchangeEnum coinank_enum.Exchange, exchangeStr string, done chan struct{}) {
	defer func() {
		klineMultiplexMu.Lock()
		delete(klineMultiplexActive, exchangeEnum)
		klineMultiplexMu.Unlock()
		close(done)
	}()

	ctx := context.Background()
	const (
		initialBackoff   = 1 * time.Second
		maxBackoff       = 30 * time.Second
		restPreFetchBars = 200
	)
	backoff := initialBackoff

	for {
		keys := getKeysForExchange(exchangeEnum)
		if len(keys) == 0 {
			return
		}

		ws, err := coinank_api.WsConn(ctx, true, false)
		if err != nil {
			logger.Warnf("⚠️ CoinAnk kline multiplex connect failed (%s): %v, retry in %s", exchangeEnum, err, backoff)
			time.Sleep(backoff)
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = initialBackoff

		// 批量订阅（多路复用：一个连接 N 个 topic）
		batch := make([]struct {
			Symbol   string
			Exchange coinank_enum.Exchange
			Interval coinank_enum.Interval
		}, 0, len(keys))
		for _, k := range keys {
			batch = append(batch, struct {
				Symbol   string
				Exchange coinank_enum.Exchange
				Interval coinank_enum.Interval
			}{k.Symbol, k.Exchange, k.Interval})
		}
		if err := ws.SubscribeBatch(batch); err != nil {
			logger.Warnf("⚠️ CoinAnk kline multiplex subscribe failed (%s): %v", exchangeEnum, err)
			_ = ws.Close()
			time.Sleep(backoff)
			continue
		}
		logger.Infof("✓ CoinAnk kline multiplex subscribed: %s, %d streams", exchangeEnum, len(keys))

		// Consume kline channel
		for msg := range ws.KlineCh {
			if msg == nil || !msg.Success {
				continue
			}
			k := msg.Data
			// 从 args 解析 symbol+interval（格式 kline@SYMBOL@exchange@interval）
			parts := strings.Split(msg.Args, "@")
			if len(parts) < 4 {
				continue
			}
			sym, intervalStr := parts[1], parts[3]
			intervalEnum, ok := mapIntervalToEnum(intervalStr)
			if !ok {
				continue
			}
			key := klineSeriesKey{Symbol: sym, Exchange: exchangeEnum, Interval: intervalEnum}
			klineStreamsMu.Lock()
			ring := klineStreams[key]
			klineStreamsMu.Unlock()
			if ring == nil {
				continue
			}
			kline := Kline{
				OpenTime:  k.StartTime,
				Open:      k.Open,
				High:      k.High,
				Low:       k.Low,
				Close:     k.Close,
				Volume:    k.Volume,
				CloseTime: k.EndTime,
			}
			ring.append(kline)
			select {
			case klineUpdateCh <- KlineUpdateEvent{Symbol: sym, Exchange: exchangeStr, Interval: intervalStr}:
			default:
			}
		}
		_ = ws.Close()

		// 断线重连前：REST 全量同步，避免丢包导致脏数据
		logger.Infof("✓ CoinAnk kline multiplex reconnecting, REST sync for %d streams", len(keys))
		for _, key := range keys {
			klineStreamsMu.Lock()
			ring := klineStreams[key]
			klineStreamsMu.Unlock()
			if ring == nil {
				continue
			}
			ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
			ts := time.Now().UnixMilli()
			coinankKlines, err := coinank_api.Kline(ctx2, key.Symbol, key.Exchange, ts, coinank_enum.To, restPreFetchBars, key.Interval)
			cancel2()
			if err == nil && len(coinankKlines) > 0 {
				klines := make([]Kline, len(coinankKlines))
				for i, k := range coinankKlines {
					klines[i] = coinankResultToKline(k)
				}
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

// getRealtimeKlines returns a recent snapshot of Klines from the WebSocket buffer, if available.
// count <= 0 means "all available".
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

// mapExchangeToEnum maps a human-readable exchange string to CoinAnk enum.
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

// mapIntervalToEnum maps timeframe string to CoinAnk interval enum.
func mapIntervalToEnum(interval string) (coinank_enum.Interval, bool) {
	switch strings.TrimSpace(interval) {
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

