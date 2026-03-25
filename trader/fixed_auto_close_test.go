package trader

import (
	"testing"
	"time"

	"nofx/store"
)

func TestRecordPositionChangeSetsFixedAutoCloseAt(t *testing.T) {
	st := setupBacktestStore(t, "nofx_fixed_auto_close_record.db")
	defer st.Close()

	at := &AutoTrader{
		id:                    "fixed-close-record",
		store:                 st,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	at.recordPositionChange("entry-1", "BTCUSDT", "LONG", "open_long", 1, 100, 10, 0, 0)

	pos, err := st.Position().GetOpenPositionBySymbol(at.id, "BTCUSDT", "LONG")
	if err != nil {
		t.Fatalf("load open position: %v", err)
	}
	if pos == nil {
		t.Fatal("expected open position to be persisted")
	}

	expectedDelta := int64(defaultRealBacktestHoldDuration / time.Millisecond)
	if pos.AutoCloseAt-pos.EntryTime != expectedDelta {
		t.Fatalf("expected auto_close_at delta %dms, got %dms", expectedDelta, pos.AutoCloseAt-pos.EntryTime)
	}
}

func TestProcessFixedAutoCloseCycleClosesExpiredPosition(t *testing.T) {
	st := setupBacktestStore(t, "nofx_fixed_auto_close_cycle.db")
	defer st.Close()

	fakeTrader := &fakeBacktestTrader{
		currentPrice:     110,
		lastSymbol:       "BTCUSDT",
		lastLeverage:     10,
		positionQuantity: 1,
	}
	at := &AutoTrader{
		id:                    "fixed-close-cycle",
		name:                  "fixed-close-cycle",
		store:                 st,
		trader:                fakeTrader,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
		fixedAutoClosingIDs:   make(map[int64]struct{}),
	}

	now := time.Now().UTC()
	pos := &store.TraderPosition{
		ID:            42,
		TraderID:      at.id,
		ExchangeID:    at.exchangeID,
		ExchangeType:  at.exchange,
		Symbol:        "BTCUSDT",
		Side:          "LONG",
		Quantity:      1,
		EntryQuantity: 1,
		EntryPrice:    100,
		EntryOrderID:  "entry-42",
		EntryTime:     now.Add(-16 * time.Minute).UnixMilli(),
		AutoCloseAt:   now.Add(-1 * time.Minute).UnixMilli(),
		Leverage:      10,
		Status:        "OPEN",
		CreatedAt:     now.Add(-16 * time.Minute).UnixMilli(),
		UpdatedAt:     now.Add(-16 * time.Minute).UnixMilli(),
	}
	if err := st.Position().CreateOpenPosition(pos); err != nil {
		t.Fatalf("create open position: %v", err)
	}

	at.processFixedAutoCloseCycle(now)

	waitForCondition(t, 2*time.Second, 25*time.Millisecond, func() bool {
		fakeTrader.mu.Lock()
		defer fakeTrader.mu.Unlock()
		return fakeTrader.closeLongCalls == 1
	})
}
