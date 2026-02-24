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
	klineStreamsMu sync.Mutex
	klineStreams   = make(map[klineSeriesKey]*klineRing)
)

// ensureKlineStream ensures there is a running WebSocket stream for the given symbol/exchange/interval.
// It is idempotent and safe to call from hot paths.
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

	go runKlineStream(key, ring, symbol, interval, exchange)
}

// runKlineStream maintains a dedicated CoinAnk kline WebSocket subscription for a single series,
// with automatic reconnection and heartbeat watchdog.
// symbolStr, intervalStr, exchangeStr 为可读字符串，用于广播 KlineUpdateEvent（数据流驱动）。
func runKlineStream(key klineSeriesKey, ring *klineRing, symbolStr, intervalStr, exchangeStr string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		initialBackoff   = 1 * time.Second
		maxBackoff       = 30 * time.Second
		heartbeatTimeout = 60 * time.Second
	)

	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		ws, err := coinank_api.WsConn(ctx, true, false)
		if err != nil {
			logger.Warnf("⚠️ CoinAnk kline WS connect failed (%s %s %s): %v, retrying in %s",
				key.Symbol, key.Exchange, key.Interval, err, backoff)
			select {
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				continue
			case <-ctx.Done():
				return
			}
		}

		// Subscribe to specific kline stream.
		if err := ws.Subscribe(key.Symbol, key.Exchange, key.Interval); err != nil {
			logger.Warnf("⚠️ CoinAnk kline WS subscribe failed (%s %s %s): %v",
				key.Symbol, key.Exchange, key.Interval, err)
			_ = ws.Close()
			select {
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				continue
			case <-ctx.Done():
				return
			}
		}

		logger.Infof("✓ CoinAnk kline WS subscribed: %s %s %s", key.Symbol, key.Exchange, key.Interval)
		backoff = initialBackoff

		lastMsgAt := time.Now()

		// Heartbeat watchdog.
		heartbeatDone := make(chan struct{})
		go func(closeFn func() error) {
			ticker := time.NewTicker(heartbeatTimeout / 2)
			defer ticker.Stop()
			defer close(heartbeatDone)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if time.Since(lastMsgAt) > heartbeatTimeout {
						logger.Warnf("⚠️ CoinAnk kline WS heartbeat timeout for %s %s %s, closing connection",
							key.Symbol, key.Exchange, key.Interval)
						_ = closeFn()
						return
					}
				}
			}
		}(ws.Close)

		// Consume kline channel (数据流：有推送才处理，不轮询)
		for msg := range ws.KlineCh {
			lastMsgAt = time.Now()
			if msg == nil || !msg.Success {
				continue
			}
			k := msg.Data
			// Convert CoinAnk KlineResult to market.Kline.
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
			// 广播：有 K 线更新，供 ATR 机器狗等按数据流触发（避免 5s 轮询与封禁）
			select {
			case klineUpdateCh <- KlineUpdateEvent{Symbol: symbolStr, Exchange: exchangeStr, Interval: intervalStr}:
			default:
				// 通道满时非阻塞丢弃，避免阻塞 WS 消费
			}
		}

		<-heartbeatDone

		// Reconnect after a delay.
		select {
		case <-time.After(backoff):
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		case <-ctx.Done():
			return
		}
	}
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

