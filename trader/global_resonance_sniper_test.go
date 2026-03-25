package trader

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nofx/store"
)

func setupGlobalSniperTestStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()

	_ = os.Remove(dbPath)

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	return st
}

func TestGlobalResonanceSniperRunsWithoutRunningTraders(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_sniper.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	if err := st.SetRealBacktestEnabled(true); err != nil {
		t.Fatalf("enable real backtest: %v", err)
	}

	cycleCh := make(chan int, 2)
	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return nil })
	sniper.scanInterval = 20 * time.Millisecond
	sniper.fillInterval = time.Hour
	sniper.onCycleStart = func(cycle int, tick time.Time, traderCount int) {
		select {
		case cycleCh <- cycle:
		default:
		}
	}

	if err := sniper.SyncWithConfig(); err != nil {
		t.Fatalf("sync sniper on: %v", err)
	}
	defer sniper.Stop()

	if !sniper.IsRunning() {
		t.Fatal("expected global resonance sniper to be running")
	}

	select {
	case cycle := <-cycleCh:
		if cycle <= 0 {
			t.Fatalf("expected positive cycle number, got %d", cycle)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected global resonance sniper to emit a cycle without running traders")
	}

	if err := st.SetRealBacktestEnabled(false); err != nil {
		t.Fatalf("disable real backtest: %v", err)
	}
	if err := sniper.SyncWithConfig(); err != nil {
		t.Fatalf("sync sniper off: %v", err)
	}
	if sniper.IsRunning() {
		t.Fatal("expected global resonance sniper to stop after config disabled")
	}
}

func TestGlobalResonanceSniperBootstrapsShadowTrackerDaemon(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_shadow_tracker.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	at := &AutoTrader{
		id:                    "shadow-tracker-bootstrap",
		store:                 st,
		stopMonitorCh:         make(chan struct{}),
		positionFirstSeenTime: make(map[string]int64),
	}

	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return []*AutoTrader{at} })
	sniper.runWatchdog = func(time.Time) error { return nil }
	sniper.runTraderCycle = func(*AutoTrader, time.Time) error { return nil }
	sniper.scanInterval = time.Hour
	sniper.fillInterval = time.Hour

	sniper.runCycle(time.Now().UTC())

	if !at.shadowTrackerRunning {
		t.Fatal("expected global sniper cycle to bootstrap shadow tracker daemon")
	}

	close(at.stopMonitorCh)
	at.monitorWg.Wait()
}

func TestGlobalResonanceSniperKeepsWatchdogRunningWhileOpenPositionsRemain(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_sniper_drain.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	now := time.Now().UTC()
	entryTime := now.Add(-10 * time.Minute).UnixMilli()

	if err := st.SetRealBacktestEnabled(false); err != nil {
		t.Fatalf("disable real backtest: %v", err)
	}
	if err := st.Position().Create(&store.TraderPosition{
		TraderID:     GlobalRealSniperTraderID,
		ExchangeID:   "acct-1",
		ExchangeType: "binance",
		Symbol:       "BTCUSDT",
		Side:         "LONG",
		Quantity:     1,
		EntryPrice:   100,
		EntryOrderID: "open-1",
		EntryTime:    entryTime,
		Leverage:     10,
		AutoCloseAt:  now.Add(5 * time.Minute).UnixMilli(),
		CreatedAt:    entryTime,
		UpdatedAt:    entryTime,
	}); err != nil {
		t.Fatalf("seed open position: %v", err)
	}

	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return nil })
	sniper.scanInterval = time.Hour
	sniper.fillInterval = time.Hour

	if err := sniper.SyncWithConfig(); err != nil {
		t.Fatalf("sync sniper with pending open positions: %v", err)
	}
	if !sniper.IsRunning() {
		t.Fatal("expected global resonance sniper to keep running in watchdog-only mode")
	}

	if err := st.Position().DeleteAllOpenPositions(GlobalRealSniperTraderID); err != nil {
		t.Fatalf("delete open positions: %v", err)
	}
	if err := sniper.SyncWithConfig(); err != nil {
		t.Fatalf("sync sniper after drain: %v", err)
	}
	if sniper.IsRunning() {
		t.Fatal("expected global resonance sniper to stop after open positions are drained")
	}
}

func TestShadowFillCycleRequiresGlobalBacktestMode(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_shadow_fill_mode.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	if err := st.SetRealBacktestEnabled(true); err != nil {
		t.Fatalf("enable real backtest: %v", err)
	}

	decisionTime := time.Now().UTC().Add(-20 * time.Minute)
	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{
		{
			TraderID:     "shadow-fill-mode",
			DecisionTime: decisionTime.UnixMilli(),
			Symbol:       "BTCUSDT",
			Sector:       "Major",
			PriceT0:      100,
			HeatScore:    80,
			CreatedAt:    decisionTime.UnixMilli(),
			UpdatedAt:    decisionTime.UnixMilli(),
		},
	}); err != nil {
		t.Fatalf("seed shadow snapshot: %v", err)
	}

	at := &AutoTrader{
		id:    "shadow-fill-mode",
		store: st,
		shadowPriceFetcher: func(symbol string, target time.Time) (float64, error) {
			return 110, nil
		},
	}

	fillNow := decisionTime.Add(20 * time.Minute)
	if err := at.processShadowFillCycle(fillNow); err != nil {
		t.Fatalf("ai shadow fill cycle: %v", err)
	}

	rows, err := st.Shadow().ListByTrader("shadow-fill-mode", 10)
	if err != nil {
		t.Fatalf("list rows after ai fill cycle: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected seeded shadow snapshot row")
	}
	if rows[0].Filled {
		t.Fatal("expected ai shadow fill daemon to skip while real backtest mode is enabled")
	}

	if err := at.processShadowFillCycleForRealBacktest(fillNow); err != nil {
		t.Fatalf("global shadow fill cycle: %v", err)
	}

	rows, err = st.Shadow().ListByTrader("shadow-fill-mode", 10)
	if err != nil {
		t.Fatalf("list rows after global fill cycle: %v", err)
	}
	if !rows[0].Filled {
		t.Fatal("expected global shadow fill cycle to mark row filled")
	}
	if rows[0].PriceT1 != 110 {
		t.Fatalf("expected filled price 110, got %.4f", rows[0].PriceT1)
	}
	if rows[0].ReturnPct != 0.1-ShadowRoundTripFeeRate {
		t.Fatalf("expected net return_pct %.6f, got %.6f", 0.1-ShadowRoundTripFeeRate, rows[0].ReturnPct)
	}
}

func TestGlobalResonanceSniperRunsWatchdogImmediatelyOnStart(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_watchdog_start.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	if err := st.SetRealBacktestEnabled(true); err != nil {
		t.Fatalf("enable real backtest: %v", err)
	}

	watchdogCh := make(chan struct{}, 1)
	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return nil })
	sniper.scanInterval = time.Hour
	sniper.fillInterval = time.Hour
	sniper.runWatchdog = func(now time.Time) error {
		select {
		case watchdogCh <- struct{}{}:
		default:
		}
		return nil
	}

	if err := sniper.SyncWithConfig(); err != nil {
		t.Fatalf("sync sniper on: %v", err)
	}
	defer sniper.Stop()

	select {
	case <-watchdogCh:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected watchdog to run immediately on start")
	}
}

func TestGlobalResonanceSniperWatchdogClosesExpiredPositions(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_watchdog_close.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	now := time.Now().UTC()
	entryTime := now.Add(-20 * time.Minute).UnixMilli()
	autoCloseAt := now.Add(-1 * time.Minute).UnixMilli()

	if err := st.Position().Create(&store.TraderPosition{
		TraderID:     GlobalRealSniperTraderID,
		ExchangeID:   "acct-1",
		ExchangeType: "binance",
		Symbol:       "BTCUSDT",
		Side:         "LONG",
		Quantity:     2,
		EntryPrice:   100,
		EntryOrderID: "open-1",
		EntryTime:    entryTime,
		Leverage:     10,
		Status:       "OPEN",
		AutoCloseAt:  autoCloseAt,
		CreatedAt:    entryTime,
		UpdatedAt:    entryTime,
	}); err != nil {
		t.Fatalf("seed open position: %v", err)
	}

	fakeTrader := &fakeBacktestTrader{
		currentPrice:     95,
		lastSymbol:       "BTCUSDT",
		lastLeverage:     10,
		positionQuantity: 2,
	}
	at := &AutoTrader{
		id:                    "executor-1",
		store:                 st,
		trader:                fakeTrader,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return []*AutoTrader{at} })
	if err := sniper.RunWatchdog(now); err != nil {
		t.Fatalf("run watchdog: %v", err)
	}

	if fakeTrader.closeLongCalls != 1 {
		t.Fatalf("expected watchdog to close position once, got %d", fakeTrader.closeLongCalls)
	}

	closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get closed positions: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(closed))
	}
	if closed[0].TraderID != GlobalRealSniperTraderID {
		t.Fatalf("expected closed position under %s, got %s", GlobalRealSniperTraderID, closed[0].TraderID)
	}
	if closed[0].ExitTime == 0 {
		t.Fatal("expected watchdog to persist exit time")
	}
}

func TestGlobalResonanceSniperWatchdogClosesExpiredPositionsWithoutAutoCloseAt(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_watchdog_close_fallback.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	now := time.Now().UTC()
	entryTime := now.Add(-20 * time.Minute).UnixMilli()

	if err := st.Position().Create(&store.TraderPosition{
		TraderID:     GlobalRealSniperTraderID,
		ExchangeID:   "acct-1",
		ExchangeType: "binance",
		Symbol:       "BTCUSDT",
		Side:         "LONG",
		Quantity:     2,
		EntryPrice:   100,
		EntryOrderID: "open-1",
		EntryTime:    entryTime,
		Leverage:     10,
		Status:       "OPEN",
		AutoCloseAt:  0,
		CreatedAt:    entryTime,
		UpdatedAt:    entryTime,
	}); err != nil {
		t.Fatalf("seed open position: %v", err)
	}

	fakeTrader := &fakeBacktestTrader{
		currentPrice:     95,
		lastSymbol:       "BTCUSDT",
		lastLeverage:     10,
		positionQuantity: 2,
	}
	at := &AutoTrader{
		id:                    "executor-1",
		store:                 st,
		trader:                fakeTrader,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return []*AutoTrader{at} })
	if err := sniper.RunWatchdog(now); err != nil {
		t.Fatalf("run watchdog: %v", err)
	}

	if fakeTrader.closeLongCalls != 1 {
		t.Fatalf("expected watchdog to close fallback-expired position once, got %d", fakeTrader.closeLongCalls)
	}

	closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get closed positions: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(closed))
	}
	if closed[0].ExitTime == 0 {
		t.Fatal("expected watchdog to persist exit time")
	}
}

func TestGlobalResonanceSniperRehydratesFutureAutoCloseTimers(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_global_resonance_rehydrate_timer.db")
	st := setupGlobalSniperTestStore(t, dbPath)
	defer st.Close()

	now := time.Now().UTC()
	entryTime := now.Add(-14 * time.Minute).UnixMilli()
	autoCloseAt := now.Add(80 * time.Millisecond).UnixMilli()

	if err := st.SetRealBacktestEnabled(false); err != nil {
		t.Fatalf("disable real backtest: %v", err)
	}
	if err := st.Position().Create(&store.TraderPosition{
		TraderID:     GlobalRealSniperTraderID,
		ExchangeID:   "acct-1",
		ExchangeType: "binance",
		Symbol:       "BTCUSDT",
		Side:         "SHORT",
		Quantity:     2,
		EntryPrice:   100,
		EntryOrderID: "open-1",
		EntryTime:    entryTime,
		AutoCloseAt:  autoCloseAt,
		Leverage:     10,
		Status:       "OPEN",
		Source:       "real_backtest",
		CreatedAt:    entryTime,
		UpdatedAt:    entryTime,
	}); err != nil {
		t.Fatalf("seed open position: %v", err)
	}

	fakeTrader := &fakeBacktestTrader{
		currentPrice:     95,
		lastSymbol:       "BTCUSDT",
		lastLeverage:     10,
		positionQuantity: -2,
	}
	at := &AutoTrader{
		id:                    "executor-1",
		store:                 st,
		trader:                fakeTrader,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	sniper := NewGlobalResonanceSniper(st, func() []*AutoTrader { return []*AutoTrader{at} })
	sniper.scanInterval = time.Hour
	sniper.fillInterval = time.Hour

	if err := sniper.SyncWithConfig(); err != nil {
		t.Fatalf("sync sniper with watchdog mode: %v", err)
	}
	defer sniper.Stop()

	waitForCondition(t, 800*time.Millisecond, 10*time.Millisecond, func() bool {
		closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
		return err == nil && len(closed) == 1
	})

	closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get closed positions: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(closed))
	}
	if fakeTrader.closeShortCalls != 1 {
		t.Fatalf("expected one close short call, got %d", fakeTrader.closeShortCalls)
	}
	deltaMs := closed[0].ExitTime - closed[0].AutoCloseAt
	if deltaMs < 0 {
		deltaMs = -deltaMs
	}
	if deltaMs > 250 {
		t.Fatalf("expected timer-based close near auto_close_at, delta=%dms exit=%d auto_close=%d", deltaMs, closed[0].ExitTime, closed[0].AutoCloseAt)
	}
}
