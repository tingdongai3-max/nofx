package market

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/logger"
	"nofx/provider/coinank/coinank_api"
)

// TickerSnapshot holds the latest funding rate and open interest snapshot for a symbol.
type TickerSnapshot struct {
	FundingRate float64
	OIUSD       float64
	UpdatedAt   time.Time
}

var (
	tickerStreamOnce   sync.Once
	tickerStreamCtx    context.Context
	tickerStreamCancel context.CancelFunc
	tickerSnapshotMap  sync.Map // map[symbol] *TickerSnapshot
)

// ensureTickerStream starts a global CoinAnk WebSocket ticker stream (idempotent).
// The underlying stream provides funding rate and open interest data for all symbols.
func ensureTickerStream() {
	tickerStreamOnce.Do(func() {
		tickerStreamCtx, tickerStreamCancel = context.WithCancel(context.Background())
		go runTickerStream(tickerStreamCtx)
	})
}

// runTickerStream connects to CoinAnk Kline WebSocket and continuously consumes ticker updates.
// It includes reconnection with backoff and an application-level heartbeat (no messages for a long
// period will force a reconnect).
func runTickerStream(ctx context.Context) {
	const (
		maxBackoff       = 30 * time.Second
		initialBackoff   = 1 * time.Second
		heartbeatTimeout = 60 * time.Second
	)

	backoff := initialBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		ws, err := coinank_api.WsConn(ctx, false, true)
		if err != nil {
			logger.Warnf("⚠️ CoinAnk ticker WS connect failed: %v, retrying in %s", err, backoff)
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

		logger.Infof("✓ CoinAnk ticker WS connected")
		backoff = initialBackoff

		lastMsgAt := time.Now()

		// Heartbeat watchdog: if we don't receive any message for heartbeatTimeout,
		// we proactively close the connection to trigger a reconnect.
		heartbeatDone := make(chan struct{})
		go func(connClose func() error) {
			ticker := time.NewTicker(heartbeatTimeout / 2)
			defer ticker.Stop()
			defer close(heartbeatDone)

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if time.Since(lastMsgAt) > heartbeatTimeout {
						logger.Warnf("⚠️ CoinAnk ticker WS heartbeat timeout (> %s), closing connection", heartbeatTimeout)
						_ = connClose()
						return
					}
				}
			}
		}(ws.Close)

		// Consume ticker channel until it is closed or context is cancelled.
		for {
			select {
			case <-ctx.Done():
				_ = ws.Close()
				<-heartbeatDone
				return
			case msg, ok := <-ws.TickersCh:
				if !ok {
					logger.Warnf("⚠️ CoinAnk ticker WS channel closed, will reconnect")
					<-heartbeatDone
					goto RECONNECT
				}

				lastMsgAt = time.Now()
				if msg == nil || !msg.Success {
					continue
				}

				symbol := strings.ToUpper(strings.TrimSpace(msg.Data.Symbol))
				if symbol == "" {
					continue
				}

				var funding float64
				if fr := strings.TrimSpace(msg.Data.FundingRate); fr != "" {
					if v, err := strconv.ParseFloat(fr, 64); err == nil {
						funding = v
					}
				}

				var oiUSD float64
				if oi := strings.TrimSpace(msg.Data.OiUSD); oi != "" {
					if v, err := strconv.ParseFloat(oi, 64); err == nil {
						oiUSD = v
					}
				}

				snapshot := &TickerSnapshot{
					FundingRate: funding,
					OIUSD:       oiUSD,
					UpdatedAt:   time.Now(),
				}
				tickerSnapshotMap.Store(symbol, snapshot)
			}
		}

	RECONNECT:
		// Short pause before next reconnect attempt.
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

// getTickerSnapshot returns the latest ticker snapshot for the given normalized symbol (e.g. BTCUSDT).
func getTickerSnapshot(symbol string) (*TickerSnapshot, bool) {
	ensureTickerStream()
	if v, ok := tickerSnapshotMap.Load(strings.ToUpper(symbol)); ok {
		if snap, ok2 := v.(*TickerSnapshot); ok2 {
			return snap, true
		}
	}
	return nil, false
}

