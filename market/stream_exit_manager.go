package market

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/store"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

type ExitDataState string

const (
	ExitDataStateWarmup      ExitDataState = "Data_Warmup"
	ExitDataStateReady       ExitDataState = "Data_Ready"
	ExitDataStateUnavailable ExitDataState = "Data_Unavailable"
)

const (
	defaultExitWarmupBars     = 20
	defaultExitRefreshSeconds = 30
	defaultExitEvalInterval   = 2 * time.Second
	defaultExitWatchdog       = 3 * time.Minute
	exitBaseSymbolBTC         = "BTCUSDT"
)

// ExitMarketSnapshot is the point-in-time market context used by the active exit engine.
type ExitMarketSnapshot struct {
	Symbol           string
	CurrentPrice     float64
	BestBidPrice     float64
	BestBidQty       float64
	BestAskPrice     float64
	BestAskQty       float64
	EMA20            float64
	ATR              float64
	Current1mRange   float64
	MeanATR          float64
	BTCChangePct     float64
	SymbolChangePct  float64
	FundingRate      float64
	DataState        ExitDataState
	BookUpdatedAt    time.Time
	KlineUpdatedAt   time.Time
	FundingUpdatedAt time.Time
	Warmed           bool
}

type ExitEvaluationFunc func(position *store.TraderPosition, snapshot ExitMarketSnapshot)
type ExitPositionProvider func() ([]*store.TraderPosition, error)

type StreamExitManagerConfig struct {
	TraderID           string
	APIClient          *APIClient
	PositionProvider   ExitPositionProvider
	EvaluateFunc       ExitEvaluationFunc
	WatchdogInterval   time.Duration
	RefreshInterval    time.Duration
	EvaluationInterval time.Duration
	WarmupBars         int
}

type exitSymbolState struct {
	mu               sync.RWMutex
	symbol           string
	bestBidPrice     float64
	bestBidQty       float64
	bestAskPrice     float64
	bestAskQty       float64
	currentPrice     float64
	bookUpdatedAt    time.Time
	closedBars       []Kline
	currentBar       *Kline
	klineUpdatedAt   time.Time
	fundingRate      float64
	fundingUpdatedAt time.Time
	warmed           bool
}

func newExitSymbolState(symbol string) *exitSymbolState {
	return &exitSymbolState{symbol: Normalize(symbol)}
}

func (s *exitSymbolState) updateBookTicker(event *futures.WsBookTickerEvent) {
	if s == nil || event == nil {
		return
	}
	bidPrice, _ := strconv.ParseFloat(event.BestBidPrice, 64)
	bidQty, _ := strconv.ParseFloat(event.BestBidQty, 64)
	askPrice, _ := strconv.ParseFloat(event.BestAskPrice, 64)
	askQty, _ := strconv.ParseFloat(event.BestAskQty, 64)

	currentPrice := 0.0
	switch {
	case bidPrice > 0 && askPrice > 0:
		currentPrice = (bidPrice + askPrice) / 2
	case bidPrice > 0:
		currentPrice = bidPrice
	case askPrice > 0:
		currentPrice = askPrice
	}

	s.mu.Lock()
	if currentPrice <= 0 {
		currentPrice = s.currentPrice
	}
	s.bestBidPrice = bidPrice
	s.bestBidQty = bidQty
	s.bestAskPrice = askPrice
	s.bestAskQty = askQty
	s.currentPrice = currentPrice
	s.bookUpdatedAt = time.Now().UTC()
	s.mu.Unlock()
}

func (s *exitSymbolState) updateKline(event *futures.WsKlineEvent, warmupBars int) {
	if s == nil || event == nil {
		return
	}

	kl := event.Kline
	openPrice, _ := strconv.ParseFloat(kl.Open, 64)
	closePrice, _ := strconv.ParseFloat(kl.Close, 64)
	highPrice, _ := strconv.ParseFloat(kl.High, 64)
	lowPrice, _ := strconv.ParseFloat(kl.Low, 64)
	volume, _ := strconv.ParseFloat(kl.Volume, 64)

	bar := Kline{
		OpenTime: kl.StartTime,
		Open:     openPrice,
		High:     highPrice,
		Low:      lowPrice,
		Close:    closePrice,
		Volume:   volume,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentBar == nil || s.currentBar.OpenTime != bar.OpenTime {
		copyBar := bar
		s.currentBar = &copyBar
	} else {
		s.currentBar.Open = bar.Open
		s.currentBar.High = math.Max(s.currentBar.High, bar.High)
		if s.currentBar.Low == 0 || bar.Low < s.currentBar.Low {
			s.currentBar.Low = bar.Low
		}
		s.currentBar.Close = bar.Close
		s.currentBar.Volume = bar.Volume
	}

	if kl.IsFinal {
		finalBar := bar
		if s.currentBar != nil {
			finalBar = *s.currentBar
		}
		finalBar.CloseTime = kl.EndTime
		s.closedBars = append(s.closedBars, finalBar)
		if len(s.closedBars) > warmupBars {
			s.closedBars = append([]Kline(nil), s.closedBars[len(s.closedBars)-warmupBars:]...)
		}
		s.currentBar = nil
		s.klineUpdatedAt = time.Now().UTC()
	}

	s.warmed = len(s.closedBars) >= warmupBars
}

func (s *exitSymbolState) replaceHistory(book *BookTicker, klines []Kline, fundingRate float64, fundingUpdatedAt time.Time, warmupBars int) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if book != nil {
		s.bestBidPrice = book.BidPrice
		s.bestBidQty = book.BidQty
		s.bestAskPrice = book.AskPrice
		s.bestAskQty = book.AskQty
		s.currentPrice = midpointPrice(book.BidPrice, book.AskPrice)
		s.bookUpdatedAt = time.Now().UTC()
	}

	if len(klines) > warmupBars {
		klines = klines[len(klines)-warmupBars:]
	}
	if len(klines) > 0 {
		s.closedBars = append([]Kline(nil), klines...)
		s.currentBar = nil
		s.warmed = len(s.closedBars) >= warmupBars
		s.klineUpdatedAt = time.Now().UTC()
	}
	if !fundingUpdatedAt.IsZero() {
		s.fundingRate = fundingRate
		s.fundingUpdatedAt = fundingUpdatedAt.UTC()
	}
}

func (s *exitSymbolState) updateFundingRate(rate float64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.fundingRate = rate
	s.fundingUpdatedAt = time.Now().UTC()
	s.mu.Unlock()
}

func (s *exitSymbolState) snapshot() (bookTicker ExitBookTickerSnapshot, bars []Kline, currentBar *Kline, fundingRate float64, fundingUpdatedAt time.Time, warmed bool, updatedAt time.Time) {
	if s == nil {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	bookTicker = ExitBookTickerSnapshot{
		BestBidPrice: s.bestBidPrice,
		BestBidQty:   s.bestBidQty,
		BestAskPrice: s.bestAskPrice,
		BestAskQty:   s.bestAskQty,
		CurrentPrice: s.currentPrice,
		UpdatedAt:    s.bookUpdatedAt,
	}
	bars = append([]Kline(nil), s.closedBars...)
	if s.currentBar != nil {
		copyBar := *s.currentBar
		currentBar = &copyBar
	}
	fundingRate = s.fundingRate
	fundingUpdatedAt = s.fundingUpdatedAt
	warmed = s.warmed
	updatedAt = s.klineUpdatedAt
	return
}

type ExitBookTickerSnapshot struct {
	BestBidPrice float64
	BestBidQty   float64
	BestAskPrice float64
	BestAskQty   float64
	CurrentPrice float64
	UpdatedAt    time.Time
}

type exitStreamSession struct {
	bookDoneC  chan struct{}
	bookStopC  chan struct{}
	klineDoneC chan struct{}
	klineStopC chan struct{}
	errCh      chan error
	stopOnce   sync.Once
}

func (s *exitStreamSession) stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		sendExitStopSignal(s.bookStopC)
		sendExitStopSignal(s.klineStopC)
	})
}

func sendExitStopSignal(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	case <-time.After(250 * time.Millisecond):
	}
}

// StreamExitManager owns Binance websocket subscriptions and market snapshots for active exits.
type StreamExitManager struct {
	traderID           string
	apiClient          *APIClient
	positionProvider   ExitPositionProvider
	evaluateFunc       ExitEvaluationFunc
	warmupBars         int
	refreshInterval    time.Duration
	evaluationInterval time.Duration
	watchdogInterval   time.Duration

	stopCh chan struct{}
	wg     sync.WaitGroup

	stateMu   sync.RWMutex
	dataState ExitDataState
	symbolMu  sync.RWMutex
	symbols   []string
	symbolMap map[string]*exitSymbolState
	sessionMu sync.Mutex
	session   *exitStreamSession

	evalSignalCh chan struct{}
}

func NewStreamExitManager(cfg StreamExitManagerConfig) *StreamExitManager {
	apiClient := cfg.APIClient
	if apiClient == nil {
		apiClient = NewAPIClient()
	}

	warmupBars := cfg.WarmupBars
	if warmupBars <= 0 {
		warmupBars = defaultExitWarmupBars
	}
	refreshInterval := cfg.RefreshInterval
	if refreshInterval <= 0 {
		refreshInterval = defaultExitRefreshSeconds * time.Second
	}
	evaluationInterval := cfg.EvaluationInterval
	if evaluationInterval <= 0 {
		evaluationInterval = defaultExitEvalInterval
	}
	watchdogInterval := cfg.WatchdogInterval
	if watchdogInterval <= 0 {
		watchdogInterval = defaultExitWatchdog
	}

	return &StreamExitManager{
		traderID:           cfg.TraderID,
		apiClient:          apiClient,
		positionProvider:   cfg.PositionProvider,
		evaluateFunc:       cfg.EvaluateFunc,
		warmupBars:         warmupBars,
		refreshInterval:    refreshInterval,
		evaluationInterval: evaluationInterval,
		watchdogInterval:   watchdogInterval,
		stopCh:             make(chan struct{}),
		dataState:          ExitDataStateWarmup,
		symbolMap:          make(map[string]*exitSymbolState),
		evalSignalCh:       make(chan struct{}, 1),
	}
}

func (m *StreamExitManager) Start() error {
	if m == nil {
		return nil
	}

	m.wg.Add(3)
	go m.supervisorLoop()
	go m.evaluationLoop()
	go m.watchdogLoop()
	return nil
}

func (m *StreamExitManager) Stop() {
	if m == nil {
		return
	}

	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}

	m.stopSession()
	m.wg.Wait()
}

func (m *StreamExitManager) DataState() ExitDataState {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.dataState
}

func (m *StreamExitManager) setDataState(state ExitDataState) {
	m.stateMu.Lock()
	m.dataState = state
	m.stateMu.Unlock()
}

func (m *StreamExitManager) signalEvaluation() {
	select {
	case m.evalSignalCh <- struct{}{}:
	default:
	}
}

func (m *StreamExitManager) stopSession() {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if m.session != nil {
		m.session.stop()
	}
}

func (m *StreamExitManager) setSession(session *exitStreamSession) {
	m.sessionMu.Lock()
	m.session = session
	m.sessionMu.Unlock()
}

func (m *StreamExitManager) getSession() *exitStreamSession {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	return m.session
}

func (m *StreamExitManager) supervisorLoop() {
	defer m.wg.Done()

	refreshTicker := time.NewTicker(m.refreshInterval)
	defer refreshTicker.Stop()

	backoff := 3 * time.Second
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		symbols := m.resolveSymbols()
		m.updateSymbolRegistry(symbols)
		m.setDataState(ExitDataStateWarmup)
		if err := m.refreshSnapshotsFromREST(symbols); err != nil {
			logger.Warnf("⚠️ [Active Exit] REST warmup failed for trader=%s: %v", m.traderID, err)
		}

		session, err := m.startStreams(symbols)
		if err != nil {
			logger.Warnf("⚠️ [Active Exit] failed to start websocket session for trader=%s: %v", m.traderID, err)
			m.setDataState(ExitDataStateUnavailable)
			select {
			case <-m.stopCh:
				return
			case <-time.After(backoff):
			}
			continue
		}

		m.setSession(session)
		if m.allSymbolsWarm(symbols) {
			m.setDataState(ExitDataStateReady)
			m.signalEvaluation()
		} else {
			m.setDataState(ExitDataStateWarmup)
		}

		restart := false
		for !restart {
			select {
			case <-m.stopCh:
				session.stop()
				return
			case <-refreshTicker.C:
				refreshedSymbols := m.resolveSymbols()
				if !sameStringSet(symbols, refreshedSymbols) {
					logger.Infof("🔄 [Active Exit] symbol set changed for trader=%s: %v -> %v", m.traderID, symbols, refreshedSymbols)
					symbols = refreshedSymbols
					m.updateSymbolRegistry(symbols)
					session.stop()
					restart = true
				} else {
					if m.allSymbolsWarm(symbols) {
						if m.DataState() != ExitDataStateReady {
							m.setDataState(ExitDataStateReady)
							m.signalEvaluation()
						}
					}
				}
			case <-session.bookDoneC:
				logger.Warnf("⚠️ [Active Exit] bookTicker stream stopped for trader=%s", m.traderID)
				m.setDataState(ExitDataStateUnavailable)
				session.stop()
				restart = true
			case <-session.klineDoneC:
				logger.Warnf("⚠️ [Active Exit] kline stream stopped for trader=%s", m.traderID)
				m.setDataState(ExitDataStateUnavailable)
				session.stop()
				restart = true
			case err := <-session.errCh:
				if err != nil {
					logger.Warnf("⚠️ [Active Exit] websocket error for trader=%s: %v", m.traderID, err)
				}
				m.setDataState(ExitDataStateUnavailable)
				session.stop()
				restart = true
			}
		}

		m.setSession(nil)
		select {
		case <-m.stopCh:
			return
		case <-time.After(backoff):
		}
	}
}

func (m *StreamExitManager) evaluationLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.evaluationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-m.evalSignalCh:
			m.evaluate(false)
		case <-ticker.C:
			m.evaluate(false)
		}
	}
}

func (m *StreamExitManager) watchdogLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if m.DataState() != ExitDataStateUnavailable {
				continue
			}
			symbols := m.resolveSymbols()
			if len(symbols) == 0 {
				continue
			}
			if err := m.refreshSnapshotsFromREST(symbols); err != nil {
				logger.Warnf("⚠️ [Active Exit] watchdog REST refresh failed for trader=%s: %v", m.traderID, err)
				continue
			}
			if m.allSymbolsWarm(symbols) {
				m.setDataState(ExitDataStateReady)
				m.signalEvaluation()
			}
		}
	}
}

func (m *StreamExitManager) resolveSymbols() []string {
	if m == nil || m.positionProvider == nil {
		return []string{exitBaseSymbolBTC}
	}

	positions, err := m.positionProvider()
	if err != nil {
		logger.Warnf("⚠️ [Active Exit] failed to load open positions for trader=%s: %v", m.traderID, err)
		return m.snapshotSymbolsFallback()
	}

	symbols := make([]string, 0, len(positions)+1)
	seen := map[string]struct{}{
		exitBaseSymbolBTC: {},
	}
	symbols = append(symbols, exitBaseSymbolBTC)
	for _, pos := range positions {
		if pos == nil {
			continue
		}
		symbol := Normalize(pos.Symbol)
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols[1:])
	return symbols
}

func (m *StreamExitManager) snapshotSymbolsFallback() []string {
	m.symbolMu.RLock()
	defer m.symbolMu.RUnlock()
	if len(m.symbols) == 0 {
		return []string{exitBaseSymbolBTC}
	}
	result := append([]string(nil), m.symbols...)
	return result
}

func (m *StreamExitManager) updateSymbolRegistry(symbols []string) {
	m.symbolMu.Lock()
	defer m.symbolMu.Unlock()
	m.symbols = append([]string(nil), symbols...)
	for _, symbol := range symbols {
		if _, ok := m.symbolMap[symbol]; !ok {
			m.symbolMap[symbol] = newExitSymbolState(symbol)
		}
	}
}

func (m *StreamExitManager) getTrackedState(symbol string) *exitSymbolState {
	m.symbolMu.RLock()
	defer m.symbolMu.RUnlock()
	if state, ok := m.symbolMap[Normalize(symbol)]; ok {
		return state
	}
	return nil
}

func (m *StreamExitManager) startStreams(symbols []string) (*exitStreamSession, error) {
	tracked := make([]string, 0, len(symbols))
	symbolIntervalPair := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		normalized := Normalize(symbol)
		if normalized == "" {
			continue
		}
		tracked = append(tracked, normalized)
		symbolIntervalPair[normalized] = "1m"
	}
	if len(tracked) == 0 {
		tracked = []string{exitBaseSymbolBTC}
		symbolIntervalPair[exitBaseSymbolBTC] = "1m"
	}

	session := &exitStreamSession{errCh: make(chan error, 4)}

	bookDoneC, bookStopC, err := futures.WsCombinedBookTickerServe(tracked, func(event *futures.WsBookTickerEvent) {
		if event == nil {
			return
		}
		state := m.getTrackedState(event.Symbol)
		if state == nil {
			return
		}
		state.updateBookTicker(event)
		if m.DataState() == ExitDataStateReady {
			m.signalEvaluation()
		}
	}, func(err error) {
		if err == nil {
			return
		}
		select {
		case session.errCh <- err:
		default:
		}
	})
	if err != nil {
		return nil, fmt.Errorf("bookTicker websocket start failed: %w", err)
	}

	klineDoneC, klineStopC, err := futures.WsCombinedKlineServe(symbolIntervalPair, func(event *futures.WsKlineEvent) {
		if event == nil {
			return
		}
		state := m.getTrackedState(event.Symbol)
		if state == nil {
			return
		}
		state.updateKline(event, m.warmupBars)
		if m.allSymbolsWarm(tracked) {
			if m.DataState() != ExitDataStateReady {
				m.setDataState(ExitDataStateReady)
			}
			m.signalEvaluation()
		}
	}, func(err error) {
		if err == nil {
			return
		}
		select {
		case session.errCh <- err:
		default:
		}
	})
	if err != nil {
		session.stop()
		return nil, fmt.Errorf("kline websocket start failed: %w", err)
	}

	session.bookDoneC = bookDoneC
	session.bookStopC = bookStopC
	session.klineDoneC = klineDoneC
	session.klineStopC = klineStopC
	return session, nil
}

func (m *StreamExitManager) refreshSnapshotsFromREST(symbols []string) error {
	if m.apiClient == nil {
		return nil
	}
	var firstErr error
	for _, symbol := range symbols {
		normalized := Normalize(symbol)
		if normalized == "" {
			continue
		}

		state := m.getTrackedState(normalized)
		if state == nil {
			state = newExitSymbolState(normalized)
			m.symbolMu.Lock()
			m.symbolMap[normalized] = state
			m.symbolMu.Unlock()
		}

		book, bookErr := m.loadBookTicker(normalized)
		if bookErr != nil && firstErr == nil {
			firstErr = bookErr
		}
		klines, klineErr := m.loadKlines(normalized)
		if klineErr != nil && firstErr == nil {
			firstErr = klineErr
		}
		fundingRate, fundingErr := m.loadFundingRate(normalized)
		if fundingErr != nil && firstErr == nil {
			firstErr = fundingErr
		}

		fundingUpdatedAt := time.Time{}
		if fundingErr == nil {
			fundingUpdatedAt = time.Now().UTC()
		}
		state.replaceHistory(book, klines, fundingRate, fundingUpdatedAt, m.warmupBars)
	}
	return firstErr
}

func (m *StreamExitManager) loadBookTicker(symbol string) (*BookTicker, error) {
	if m.apiClient == nil {
		return nil, nil
	}
	ticker, err := m.apiClient.GetBookTicker(symbol)
	if err != nil {
		return nil, err
	}
	if ticker == nil {
		return nil, nil
	}
	ticker.Symbol = Normalize(ticker.Symbol)
	return ticker, nil
}

func (m *StreamExitManager) loadKlines(symbol string) ([]Kline, error) {
	if m.apiClient == nil {
		return nil, nil
	}
	klines, err := m.apiClient.GetKlines(symbol, "1m", m.warmupBars)
	if err != nil {
		return nil, err
	}
	result := make([]Kline, 0, len(klines))
	for _, kline := range klines {
		result = append(result, kline)
	}
	return result, nil
}

func (m *StreamExitManager) loadFundingRate(symbol string) (float64, error) {
	if m.apiClient == nil {
		return 0, nil
	}
	return m.apiClient.GetFundingRate(symbol)
}

func (m *StreamExitManager) allSymbolsWarm(symbols []string) bool {
	if len(symbols) == 0 {
		return false
	}
	for _, symbol := range symbols {
		state := m.getTrackedState(symbol)
		if state == nil {
			return false
		}
		_, bars, _, _, _, warmed, _ := state.snapshot()
		if !warmed || len(bars) < m.warmupBars {
			return false
		}
	}
	return true
}

func (m *StreamExitManager) evaluate(forceFallback bool) {
	if m.positionProvider == nil || m.evaluateFunc == nil {
		return
	}

	if !forceFallback && m.DataState() != ExitDataStateReady {
		return
	}

	positions, err := m.positionProvider()
	if err != nil {
		logger.Warnf("⚠️ [Active Exit] failed to load positions during evaluation for trader=%s: %v", m.traderID, err)
		return
	}
	if len(positions) == 0 {
		return
	}

	for _, pos := range positions {
		if pos == nil || strings.ToUpper(pos.Status) != "OPEN" {
			continue
		}
		snapshot, ok := m.snapshotForPosition(pos)
		if !ok {
			continue
		}
		go m.evaluateFunc(pos, snapshot)
	}
}

func (m *StreamExitManager) snapshotForPosition(pos *store.TraderPosition) (ExitMarketSnapshot, bool) {
	if pos == nil {
		return ExitMarketSnapshot{}, false
	}

	symbol := Normalize(pos.Symbol)
	state := m.getTrackedState(symbol)
	if state == nil {
		return ExitMarketSnapshot{}, false
	}
	book, bars, currentBar, fundingRate, fundingUpdatedAt, warmed, klineUpdatedAt := state.snapshot()
	if !warmed || len(bars) < m.warmupBars {
		return ExitMarketSnapshot{}, false
	}

	currentPrice := book.CurrentPrice
	if currentPrice <= 0 {
		if len(bars) > 0 {
			currentPrice = bars[len(bars)-1].Close
		}
	}
	if currentPrice <= 0 {
		return ExitMarketSnapshot{}, false
	}

	ema20 := computeEMA20(bars)
	atr := computeATR20(bars)
	currentRange := computeCurrentRange(currentBar, bars)
	meanATR := atr

	btcChangePct := 0.0
	symbolChangePct := computeChangePct(currentPrice, bars)
	if btcState := m.getTrackedState(exitBaseSymbolBTC); btcState != nil {
		btcBook, btcBars, _, _, _, btcWarm, _ := btcState.snapshot()
		if btcWarm && len(btcBars) >= m.warmupBars {
			btcPrice := btcBook.CurrentPrice
			if btcPrice <= 0 && len(btcBars) > 0 {
				btcPrice = btcBars[len(btcBars)-1].Close
			}
			btcChangePct = computeChangePct(btcPrice, btcBars)
		}
	}

	return ExitMarketSnapshot{
		Symbol:           symbol,
		CurrentPrice:     currentPrice,
		BestBidPrice:     book.BestBidPrice,
		BestBidQty:       book.BestBidQty,
		BestAskPrice:     book.BestAskPrice,
		BestAskQty:       book.BestAskQty,
		EMA20:            ema20,
		ATR:              atr,
		Current1mRange:   currentRange,
		MeanATR:          meanATR,
		BTCChangePct:     btcChangePct,
		SymbolChangePct:  symbolChangePct,
		FundingRate:      fundingRate,
		DataState:        m.DataState(),
		BookUpdatedAt:    book.UpdatedAt,
		KlineUpdatedAt:   klineUpdatedAt,
		FundingUpdatedAt: fundingUpdatedAt,
		Warmed:           warmed,
	}, true
}

// SnapshotForPosition exposes the current exit snapshot for external consumers.
func (m *StreamExitManager) SnapshotForPosition(pos *store.TraderPosition) (ExitMarketSnapshot, bool) {
	return m.snapshotForPosition(pos)
}

func computeEMA20(bars []Kline) float64 {
	if len(bars) < 20 {
		return 0
	}

	closes := make([]Kline, 0, len(bars))
	closes = append(closes, bars...)
	return calculateEMA(closes, 20)
}

func computeATR20(bars []Kline) float64 {
	if len(bars) == 0 {
		return 0
	}

	trs := make([]float64, 0, len(bars))
	for i, bar := range bars {
		if bar.High <= 0 || bar.Low <= 0 {
			trs = append(trs, 0)
			continue
		}
		tr := bar.High - bar.Low
		if i > 0 {
			prevClose := bars[i-1].Close
			tr = math.Max(tr, math.Max(math.Abs(bar.High-prevClose), math.Abs(bar.Low-prevClose)))
		}
		trs = append(trs, tr)
	}

	sum := 0.0
	for _, tr := range trs {
		sum += tr
	}
	return sum / float64(len(trs))
}

func computeCurrentRange(currentBar *Kline, bars []Kline) float64 {
	if currentBar != nil {
		return math.Max(0, currentBar.High-currentBar.Low)
	}
	if len(bars) == 0 {
		return 0
	}
	last := bars[len(bars)-1]
	return math.Max(0, last.High-last.Low)
}

func computeChangePct(current float64, bars []Kline) float64 {
	if current <= 0 || len(bars) == 0 {
		return 0
	}
	base := bars[0].Close
	if base <= 0 {
		return 0
	}
	return ((current - base) / base) * 100
}

func midpointPrice(bid, ask float64) float64 {
	switch {
	case bid > 0 && ask > 0:
		return (bid + ask) / 2
	case bid > 0:
		return bid
	case ask > 0:
		return ask
	default:
		return 0
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}
