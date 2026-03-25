package trader

import (
	"nofx/kernel"
	"nofx/logger"
	"nofx/store"
	"sort"
	"sync"
	"time"
)

const defaultGlobalShadowFillInterval = 1 * time.Minute
const defaultGlobalEntryFloorOptimizeInterval = 12 * time.Hour

type GlobalResonanceSniper struct {
	store          *store.Store
	traderProvider func() []*AutoTrader

	scanInterval          time.Duration
	fillInterval          time.Duration
	floorOptimizeInterval time.Duration
	now                   func() time.Time

	runTraderCycle    func(*AutoTrader, time.Time) error
	runShadowFill     func(time.Time) error
	runWatchdog       func(time.Time) error
	runFloorOptimizer func(time.Time) error
	onCycleStart      func(int, time.Time, int)

	mu                             sync.Mutex
	timerBootstrapMu               sync.Mutex
	realBacktestTimersBootstrapped bool
	running                        bool
	stopCh                         chan struct{}
	doneCh                         chan struct{}
	cycleCount                     int
}

func NewGlobalResonanceSniper(st *store.Store, traderProvider func() []*AutoTrader) *GlobalResonanceSniper {
	return &GlobalResonanceSniper{
		store:                 st,
		traderProvider:        traderProvider,
		scanInterval:          defaultRealBacktestScanInterval,
		fillInterval:          defaultGlobalShadowFillInterval,
		floorOptimizeInterval: defaultGlobalEntryFloorOptimizeInterval,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (s *GlobalResonanceSniper) Start() bool {
	if s == nil {
		return false
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	s.stopCh = stopCh
	s.doneCh = doneCh
	s.running = true

	interval := s.getScanInterval()
	delay, nextTick := calculateNextAlignmentFrom(s.currentTime(), interval)
	fillInterval := s.getFillInterval()
	s.mu.Unlock()

	logger.Info("Global Resonance Sniper started")
	logger.Infof("🎯 Global Resonance Sniper armed (scan=%s, fill=%s, next=%s)",
		interval,
		fillInterval,
		nextTick.Format(time.RFC3339),
	)

	if err := s.bootstrapRealBacktestAutoCloseTimers(s.currentTime()); err != nil {
		logger.Warnf("⚠️ Failed to bootstrap real backtest auto-close timers: %v", err)
	}
	if err := s.runFloorOptimizerCycle(s.currentTime()); err != nil {
		logger.Warnf("⚠️ Failed to optimize adaptive entry floor on startup: %v", err)
	}
	s.bootstrapShadowTrackerDaemons()

	go s.loop(stopCh, doneCh, delay, nextTick)
	return true
}

func (s *GlobalResonanceSniper) Stop() bool {
	if s == nil {
		return false
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return false
	}

	stopCh := s.stopCh
	doneCh := s.doneCh
	s.stopCh = nil
	s.doneCh = nil
	s.running = false
	s.mu.Unlock()

	close(stopCh)
	<-doneCh
	s.timerBootstrapMu.Lock()
	s.realBacktestTimersBootstrapped = false
	s.timerBootstrapMu.Unlock()
	logger.Info("Global Resonance Sniper stopped")
	return true
}

func (s *GlobalResonanceSniper) IsRunning() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *GlobalResonanceSniper) SyncWithConfig() error {
	if s == nil || s.store == nil {
		return nil
	}

	enabled, err := s.store.GetRealBacktestEnabled()
	if err != nil {
		return err
	}
	if enabled {
		s.Start()
		return nil
	}

	openPositions, err := s.store.Position().GetOpenPositions(GlobalRealSniperTraderID)
	if err != nil {
		return err
	}
	if len(openPositions) > 0 {
		if s.Start() {
			logger.Infof("🎯 Global resonance sniper entering watchdog-only mode: %d open real backtest position(s) remain", len(openPositions))
		}
		return nil
	}
	s.Stop()
	return nil
}

func (s *GlobalResonanceSniper) loop(stopCh <-chan struct{}, doneCh chan<- struct{}, delay time.Duration, nextTick time.Time) {
	defer close(doneCh)

	scanInterval := s.getScanInterval()
	fillTicker := time.NewTicker(s.getFillInterval())
	defer fillTicker.Stop()
	floorTicker := time.NewTicker(s.getFloorOptimizeInterval())
	defer floorTicker.Stop()

	if err := s.runWatchdogCycle(s.currentTime()); err != nil {
		logger.Warnf("⚠️ Global resonance watchdog failed on startup: %v", err)
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			scheduledTick := nextTick.UTC()
			nextDelay, alignedNextTick := calculateNextAlignmentFrom(scheduledTick, scanInterval)
			nextTick = alignedNextTick
			timer.Reset(nextDelay)
			s.runCycle(scheduledTick)
		case fillTime := <-fillTicker.C:
			if err := s.runFill(fillTime.UTC()); err != nil {
				logger.Warnf("⚠️ Global resonance shadow fill failed: %v", err)
			}
		case floorTime := <-floorTicker.C:
			if err := s.runFloorOptimizerCycle(floorTime.UTC()); err != nil {
				logger.Warnf("⚠️ Global resonance entry floor optimization failed: %v", err)
			}
		case <-stopCh:
			return
		}
	}
}

func (s *GlobalResonanceSniper) runCycle(tickTime time.Time) {
	traders := s.snapshotTraders()
	s.bootstrapShadowTrackerDaemonsFrom(traders)

	s.mu.Lock()
	s.cycleCount++
	cycleNumber := s.cycleCount
	hook := s.onCycleStart
	s.mu.Unlock()

	if hook != nil {
		hook(cycleNumber, tickTime, len(traders))
	}

	logger.Infof("🎯 Real Backtest Cycle #%d (tick=%s, traders=%d)",
		cycleNumber,
		tickTime.Format(time.RFC3339),
		len(traders),
	)

	if err := s.runWatchdogCycle(tickTime); err != nil {
		logger.Warnf("⚠️ Real backtest watchdog failed: %v", err)
	}

	if len(traders) == 0 {
		logger.Infof("🎯 Real Backtest Cycle #%d skipped: no loaded traders", cycleNumber)
		return
	}

	runner := s.runTraderCycle
	if runner == nil {
		runner = func(at *AutoTrader, tick time.Time) error {
			return at.runRealBacktestCycle(tick)
		}
	}

	eligible := 0
	for _, at := range traders {
		if at == nil || at.store == nil || at.strategyEngine == nil || at.IsGridStrategy() {
			continue
		}
		eligible++
		if err := runner(at, tickTime); err != nil {
			logger.Warnf("⚠️ Global resonance cycle failed for trader %s (%s): %v",
				at.GetName(),
				at.GetID(),
				err,
			)
		}
	}

	if eligible == 0 {
		logger.Infof("🎯 Real Backtest Cycle #%d skipped: no eligible non-grid traders", cycleNumber)
	}
}

func (s *GlobalResonanceSniper) runFill(now time.Time) error {
	runner := s.runShadowFill
	if runner == nil {
		runner = func(fillTime time.Time) error {
			return processShadowFillCycleWithStore(s.store, fillTime, defaultShadowTargetWindow, nil)
		}
	}
	return runner(now)
}

func (s *GlobalResonanceSniper) runWatchdogCycle(now time.Time) error {
	runner := s.runWatchdog
	if runner == nil {
		runner = s.RunWatchdog
	}
	return runner(now)
}

func (s *GlobalResonanceSniper) runFloorOptimizerCycle(now time.Time) error {
	runner := s.runFloorOptimizer
	if runner == nil {
		runner = func(time.Time) error {
			_, err := kernel.OptimizeEntryFloor(s.store)
			return err
		}
	}
	return runner(now)
}

func (s *GlobalResonanceSniper) bootstrapShadowTrackerDaemons() {
	s.bootstrapShadowTrackerDaemonsFrom(s.snapshotTraders())
}

func (s *GlobalResonanceSniper) bootstrapShadowTrackerDaemonsFrom(traders []*AutoTrader) {
	if len(traders) == 0 {
		return
	}

	for _, at := range traders {
		if at == nil {
			continue
		}
		at.startShadowTrackerDaemon()
	}
}

func (s *GlobalResonanceSniper) RunWatchdog(now time.Time) error {
	if s == nil || s.store == nil {
		return nil
	}

	if err := s.bootstrapRealBacktestAutoCloseTimers(now.UTC()); err != nil {
		return err
	}

	expired, err := s.store.Position().GetExpiredAutoClosePositions(
		GlobalRealSniperTraderID,
		now.UTC().UnixMilli(),
		int64(defaultRealBacktestHoldDuration/time.Millisecond),
	)
	if err != nil {
		return err
	}
	if len(expired) == 0 {
		return nil
	}

	executors := s.executorsByExchangeID()
	for _, pos := range expired {
		if pos == nil {
			continue
		}
		at := executors[pos.ExchangeID]
		if at == nil {
			logger.Warnf("⚠️ Watchdog cannot close %s %s: no executor for exchange %s", pos.Symbol, pos.Side, pos.ExchangeID)
			continue
		}
		if err := at.forceCloseExpiredRealBacktestPosition(pos); err != nil {
			logger.Warnf("⚠️ Watchdog close failed for %s %s: %v", pos.Symbol, pos.Side, err)
			continue
		}
		logger.Infof("🎯 Watchdog closed overdue real backtest position: %s %s auto_close_at=%d",
			pos.Symbol,
			pos.Side,
			pos.AutoCloseAt,
		)
	}
	return nil
}

func (s *GlobalResonanceSniper) bootstrapRealBacktestAutoCloseTimers(now time.Time) error {
	if s == nil || s.store == nil {
		return nil
	}

	s.timerBootstrapMu.Lock()
	defer s.timerBootstrapMu.Unlock()
	if s.realBacktestTimersBootstrapped {
		return nil
	}

	openPositions, err := s.store.Position().GetOpenPositions(GlobalRealSniperTraderID)
	if err != nil {
		return err
	}
	if len(openPositions) == 0 {
		s.realBacktestTimersBootstrapped = true
		return nil
	}

	executors := s.executorsByExchangeID()
	nowMs := now.UTC().UnixMilli()
	scheduled := 0
	closed := 0
	skipped := 0

	for _, pos := range openPositions {
		if pos == nil {
			continue
		}

		executor := s.executorForOpenPosition(pos, executors)
		if executor == nil {
			skipped++
			logger.Warnf("⚠️ Real backtest auto-close bootstrap skipped %s %s: no executor for exchange %s",
				pos.Symbol,
				pos.Side,
				pos.ExchangeID,
			)
			continue
		}

		autoCloseAt := pos.AutoCloseAt
		if autoCloseAt <= 0 && pos.EntryTime > 0 {
			autoCloseAt = pos.EntryTime + int64(defaultRealBacktestHoldDuration/time.Millisecond)
		}

		if autoCloseAt > 0 && autoCloseAt <= nowMs {
			closed++
			if err := executor.forceCloseExpiredRealBacktestPositionByID(pos.ID); err != nil {
				logger.Warnf("⚠️ Real backtest bootstrap close failed for %s %s: %v", pos.Symbol, pos.Side, err)
			}
			continue
		}

		if autoCloseAt > 0 {
			scheduled++
			executor.armRealBacktestAutoCloseTimer(pos, autoCloseAt)
		}
	}

	s.realBacktestTimersBootstrapped = true

	logger.Infof("🎯 Real backtest auto-close timers bootstrapped: scheduled=%d closed=%d skipped=%d", scheduled, closed, skipped)
	return nil
}

func (s *GlobalResonanceSniper) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *GlobalResonanceSniper) getScanInterval() time.Duration {
	if s != nil && s.scanInterval > 0 {
		return s.scanInterval
	}
	return defaultRealBacktestScanInterval
}

func (s *GlobalResonanceSniper) getFillInterval() time.Duration {
	if s != nil && s.fillInterval > 0 {
		return s.fillInterval
	}
	return defaultGlobalShadowFillInterval
}

func (s *GlobalResonanceSniper) getFloorOptimizeInterval() time.Duration {
	if s != nil && s.floorOptimizeInterval > 0 {
		return s.floorOptimizeInterval
	}
	return defaultGlobalEntryFloorOptimizeInterval
}

func (s *GlobalResonanceSniper) snapshotTraders() []*AutoTrader {
	if s == nil || s.traderProvider == nil {
		return nil
	}
	rows := s.traderProvider()
	if len(rows) == 0 {
		return nil
	}

	traders := make([]*AutoTrader, 0, len(rows))
	for _, at := range rows {
		if at != nil {
			traders = append(traders, at)
		}
	}
	sort.SliceStable(traders, func(i, j int) bool {
		return traders[i].GetID() < traders[j].GetID()
	})
	return traders
}

func (s *GlobalResonanceSniper) executorsByExchangeID() map[string]*AutoTrader {
	traders := s.snapshotTraders()
	if len(traders) == 0 {
		return nil
	}

	executors := make(map[string]*AutoTrader, len(traders))
	for _, at := range traders {
		if at == nil {
			continue
		}
		exchangeID := at.GetExchangeID()
		if exchangeID == "" {
			exchangeID = at.GetID()
		}
		if _, exists := executors[exchangeID]; !exists {
			executors[exchangeID] = at
		}
	}
	return executors
}

func (s *GlobalResonanceSniper) executorForOpenPosition(pos *store.TraderPosition, executors map[string]*AutoTrader) *AutoTrader {
	if s == nil || pos == nil || len(executors) == 0 {
		return nil
	}

	if executor := executors[pos.ExchangeID]; executor != nil {
		return executor
	}
	if executor := executors[pos.TraderID]; executor != nil {
		return executor
	}
	if len(executors) == 1 {
		for _, executor := range executors {
			return executor
		}
	}
	return nil
}
