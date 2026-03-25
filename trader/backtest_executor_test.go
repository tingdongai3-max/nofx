package trader

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

type fakeBacktestTrader struct {
	mu                sync.Mutex
	openLongCalls     int
	openShortCalls    int
	closeLongCalls    int
	closeShortCalls   int
	stopLossCalls     int
	takeProfitCalls   int
	setMarginCalls    int
	lastMarginIsCross bool
	lastSymbol        string
	lastQuantity      float64
	lastLeverage      int
	currentPrice      float64
	positionQuantity  float64
	closeLongBlock    chan struct{}
	closeShortBlock   chan struct{}
}

func (f *fakeBacktestTrader) GetBalance() (map[string]interface{}, error) {
	return map[string]interface{}{
		"totalEquity":      1000.0,
		"availableBalance": 1000.0,
	}, nil
}

func (f *fakeBacktestTrader) GetPositions() ([]map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	side := "long"
	quantity := f.positionQuantity
	if quantity < 0 {
		side = "short"
	}
	return []map[string]interface{}{
		{
			"symbol":           f.lastSymbol,
			"side":             side,
			"entryPrice":       100.0,
			"markPrice":        f.currentPrice,
			"positionAmt":      quantity,
			"unRealizedProfit": 0.0,
			"liquidationPrice": 0.0,
			"leverage":         float64(f.lastLeverage),
			"createdTime":      time.Now().UTC().UnixMilli(),
		},
	}, nil
}

func (f *fakeBacktestTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openLongCalls++
	f.lastSymbol = symbol
	f.lastQuantity = quantity
	f.lastLeverage = leverage
	f.positionQuantity = quantity
	return map[string]interface{}{"orderId": int64(11)}, nil
}

func (f *fakeBacktestTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openShortCalls++
	f.lastSymbol = symbol
	f.lastQuantity = quantity
	f.lastLeverage = leverage
	f.positionQuantity = -quantity
	return map[string]interface{}{"orderId": int64(12)}, nil
}

func (f *fakeBacktestTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	if f.closeLongBlock != nil {
		<-f.closeLongBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeLongCalls++
	f.positionQuantity = 0
	return map[string]interface{}{"orderId": int64(21)}, nil
}

func (f *fakeBacktestTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	if f.closeShortBlock != nil {
		<-f.closeShortBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeShortCalls++
	f.positionQuantity = 0
	return map[string]interface{}{"orderId": int64(22)}, nil
}

func (f *fakeBacktestTrader) SetLeverage(symbol string, leverage int) error { return nil }

func (f *fakeBacktestTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setMarginCalls++
	f.lastMarginIsCross = isCrossMargin
	return nil
}

func (f *fakeBacktestTrader) GetMarketPrice(symbol string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.currentPrice, nil
}

func (f *fakeBacktestTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopLossCalls++
	return nil
}

func (f *fakeBacktestTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.takeProfitCalls++
	return nil
}

func (f *fakeBacktestTrader) CancelStopLossOrders(symbol string) error   { return nil }
func (f *fakeBacktestTrader) CancelTakeProfitOrders(symbol string) error { return nil }
func (f *fakeBacktestTrader) CancelAllOrders(symbol string) error        { return nil }
func (f *fakeBacktestTrader) CancelStopOrders(symbol string) error       { return nil }
func (f *fakeBacktestTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	return "", nil
}
func (f *fakeBacktestTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	return map[string]interface{}{"status": "FILLED"}, nil
}
func (f *fakeBacktestTrader) GetClosedPnL(startTime time.Time, limit int) ([]ClosedPnLRecord, error) {
	return nil, nil
}
func (f *fakeBacktestTrader) GetOpenOrders(symbol string) ([]OpenOrder, error) { return nil, nil }

func setupBacktestStore(t *testing.T, name string) *store.Store {
	t.Helper()

	dbPath := filepath.Join("/tmp", name)
	_ = os.Remove(dbPath)

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func waitForCondition(t *testing.T, timeout time.Duration, interval time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatal("condition was not met before timeout")
}

func seedArchetypeBacktestRows(t *testing.T, st *store.Store, traderID string, baseTime int64) {
	t.Helper()

	rows := make([]*store.ShadowSnapshot, 0, 30)
	appendRows := func(symbol, sector string, heatScore, returnPct float64, offset int64, scores map[string]float64, count int) {
		available := make(map[string]bool, len(scores))
		for key := range scores {
			available[key] = true
		}
		for i := 0; i < count; i++ {
			rows = append(rows, &store.ShadowSnapshot{
				TraderID:     traderID,
				DecisionTime: baseTime + offset + int64(i),
				Symbol:       symbol,
				Sector:       sector,
				HeatScore:    heatScore,
				PriceT0:      100,
				PriceT1:      100 * (1 + returnPct),
				ReturnPct:    returnPct,
				Filled:       true,
				FilledAt:     baseTime + offset + 1000 + int64(i),
				CreatedAt:    baseTime + offset + int64(i),
				UpdatedAt:    baseTime + offset + 1000 + int64(i),
				RawFactors: store.ShadowRawFactors{
					Scores:    scores,
					Available: available,
				}.MarshalText(),
			})
		}
	}

	appendRows("RANGEUSDT", "AI", 76, 0.03, 0, map[string]float64{
		"market":              0.2,
		"trend":               0.1,
		"donchian_factor":     0.1,
		"volume_spike":        0.3,
		"mtf_resonance":       0.9,
		"quant_netflow":       0.1,
		"vol_utilization":     10,
		"funding_rate":        1,
		"orderbook_imbalance": 6,
	}, 10)
	appendRows("PULSEUSDT", "AI", 90, 0.08, 100, map[string]float64{
		"market":              1.4,
		"trend":               2.2,
		"donchian_factor":     1.8,
		"volume_spike":        3.2,
		"mtf_resonance":       1.7,
		"quant_netflow":       1.4,
		"vol_utilization":     55,
		"funding_rate":        3,
		"orderbook_imbalance": 18,
	}, 10)
	appendRows("DUMPUSDT", "AI", 72, 0.05, 200, map[string]float64{
		"market":              -2.4,
		"trend":               -2.0,
		"donchian_factor":     -2.1,
		"volume_spike":        -0.2,
		"mtf_resonance":       0.5,
		"quant_netflow":       -1.5,
		"vol_utilization":     8,
		"funding_rate":        -2,
		"orderbook_imbalance": -5,
	}, 10)

	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("seed archetype rows: %v", err)
	}
}

func buildTypeBMarketData() *market.Data {
	return &market.Data{
		Symbol:       "PULSEUSDT",
		Sector:       "AI",
		CurrentPrice: 100,
		HeatScore: &market.HeatScoreData{
			RawFactorScores: map[string]float64{
				"market":          1.5,
				"trend":           2.1,
				"donchian_factor": 1.7,
				"volume_spike":    3.1,
				"mtf_resonance":   1.6,
				"quant_netflow":   1.5,
			},
			RawFactorAvailable: map[string]bool{
				"market":          true,
				"trend":           true,
				"donchian_factor": true,
				"volume_spike":    true,
				"mtf_resonance":   true,
				"quant_netflow":   true,
			},
		},
		VolatilityUtilization: 0.56,
		FundingRate:           0.0003,
		Orderbook: &market.OrderbookData{
			Imbalance: 0.19,
		},
	}
}

func buildMutantMarketData() *market.Data {
	return &market.Data{
		Symbol:       "PULSEUSDT",
		Sector:       "AI",
		CurrentPrice: 100,
		HeatScore: &market.HeatScoreData{
			RawFactorScores: map[string]float64{
				"market":          8.0,
				"trend":           -7.0,
				"donchian_factor": 6.0,
				"volume_spike":    0.1,
				"mtf_resonance":   4.5,
				"quant_netflow":   5.0,
			},
			RawFactorAvailable: map[string]bool{
				"market":          true,
				"trend":           true,
				"donchian_factor": true,
				"volume_spike":    true,
				"mtf_resonance":   true,
				"quant_netflow":   true,
			},
		},
		VolatilityUtilization: 0.95,
		FundingRate:           0.0020,
		Orderbook: &market.OrderbookData{
			Imbalance: -0.42,
		},
	}
}

func momentumRadiusRawFactors(scores map[string]float64) string {
	available := make(map[string]bool, len(scores))
	for key := range scores {
		available[key] = true
	}
	return store.ShadowRawFactors{
		Scores:    scores,
		Available: available,
	}.MarshalText()
}

func seedRadiusRiskFloorRows(t *testing.T, st *store.Store, traderID string, baseTime int64) {
	t.Helper()

	typeA := map[string]float64{
		"market":              0.20,
		"trend":               0.10,
		"donchian_factor":     0.10,
		"volume_spike":        0.30,
		"mtf_resonance":       0.90,
		"quant_netflow":       0.10,
		"vol_utilization":     10,
		"funding_rate":        1.0,
		"orderbook_imbalance": 6,
	}
	nearPositive := map[string]float64{
		"market":              1.50,
		"trend":               2.10,
		"donchian_factor":     1.70,
		"volume_spike":        3.10,
		"mtf_resonance":       1.60,
		"quant_netflow":       1.50,
		"vol_utilization":     56,
		"funding_rate":        3.0,
		"orderbook_imbalance": 19,
	}
	farPositive := map[string]float64{
		"market":              2.10,
		"trend":               2.55,
		"donchian_factor":     2.20,
		"volume_spike":        3.95,
		"mtf_resonance":       1.95,
		"quant_netflow":       2.00,
		"vol_utilization":     63,
		"funding_rate":        4.0,
		"orderbook_imbalance": 24,
	}
	typeC := map[string]float64{
		"market":              -2.40,
		"trend":               -2.00,
		"donchian_factor":     -2.10,
		"volume_spike":        -0.20,
		"mtf_resonance":       0.50,
		"quant_netflow":       -1.50,
		"vol_utilization":     8,
		"funding_rate":        -2.0,
		"orderbook_imbalance": -5,
	}

	rows := make([]*store.ShadowSnapshot, 0, 42)
	appendRow := func(symbol string, offset int64, scores map[string]float64, returnPct float64, tag string, archetypeLabel string) {
		rows = append(rows, &store.ShadowSnapshot{
			TraderID:       traderID,
			DecisionTime:   baseTime + offset,
			Symbol:         symbol,
			Sector:         "AI",
			HeatScore:      90,
			PriceT0:        100,
			PriceT1:        100 * (1 + returnPct),
			ReturnPct:      returnPct,
			Filled:         true,
			FilledAt:       baseTime + offset + 1000,
			CreatedAt:      baseTime + offset,
			UpdatedAt:      baseTime + offset + 1000,
			RawFactors:     momentumRadiusRawFactors(scores),
			KnnAuditTag:    tag,
			ArchetypeLabel: archetypeLabel,
		})
	}

	for i := 0; i < 10; i++ {
		scores := map[string]float64{
			"market":              typeA["market"] + float64(i%3)*0.02,
			"trend":               typeA["trend"] + float64(i%2)*0.02,
			"donchian_factor":     typeA["donchian_factor"] + float64(i%2)*0.02,
			"volume_spike":        typeA["volume_spike"] + float64(i%3)*0.03,
			"mtf_resonance":       typeA["mtf_resonance"] + float64(i%2)*0.04,
			"quant_netflow":       typeA["quant_netflow"] + float64(i%3)*0.02,
			"vol_utilization":     typeA["vol_utilization"] + float64(i%2),
			"funding_rate":        typeA["funding_rate"] + float64(i%2)*0.1,
			"orderbook_imbalance": typeA["orderbook_imbalance"] + float64(i%2),
		}
		appendRow("RANGEUSDT", int64(i), scores, 0.005, "", "Type_A")
	}
	for i := 0; i < 8; i++ {
		scores := map[string]float64{
			"market":              farPositive["market"] + float64(i%3)*0.12,
			"trend":               farPositive["trend"] + float64(i%4)*0.10,
			"donchian_factor":     farPositive["donchian_factor"] + float64(i%3)*0.09,
			"volume_spike":        farPositive["volume_spike"] + float64(i%4)*0.14,
			"mtf_resonance":       farPositive["mtf_resonance"] + float64(i%2)*0.08,
			"quant_netflow":       farPositive["quant_netflow"] + float64(i%3)*0.07,
			"vol_utilization":     farPositive["vol_utilization"] + float64(i%2)*2,
			"funding_rate":        farPositive["funding_rate"] + float64(i%2)*0.2,
			"orderbook_imbalance": farPositive["orderbook_imbalance"] + float64(i%3),
		}
		appendRow("PULSEUSDT", 100+int64(i), scores, 0.005, "", "Type_B")
	}
	for i := 0; i < 10; i++ {
		scores := map[string]float64{
			"market":              typeC["market"] - float64(i%3)*0.06,
			"trend":               typeC["trend"] - float64(i%2)*0.05,
			"donchian_factor":     typeC["donchian_factor"] - float64(i%2)*0.05,
			"volume_spike":        typeC["volume_spike"] + float64(i%2)*0.02,
			"mtf_resonance":       typeC["mtf_resonance"] + float64(i%2)*0.03,
			"quant_netflow":       typeC["quant_netflow"] - float64(i%2)*0.05,
			"vol_utilization":     typeC["vol_utilization"] + float64(i%2),
			"funding_rate":        typeC["funding_rate"] - float64(i%2)*0.1,
			"orderbook_imbalance": typeC["orderbook_imbalance"] - float64(i%2),
		}
		appendRow("DUMPUSDT", 200+int64(i), scores, 0.005, "", "Type_C")
	}
	for i := 0; i < 4; i++ {
		scores := map[string]float64{
			"market":              nearPositive["market"] + float64(i%2)*0.005,
			"trend":               nearPositive["trend"] - float64(i%2)*0.010,
			"donchian_factor":     nearPositive["donchian_factor"] + float64(i%2)*0.008,
			"volume_spike":        nearPositive["volume_spike"] + float64(i%2)*0.010,
			"mtf_resonance":       nearPositive["mtf_resonance"] + float64(i%2)*0.008,
			"quant_netflow":       nearPositive["quant_netflow"] + float64(i%2)*0.008,
			"vol_utilization":     nearPositive["vol_utilization"] + float64(i%2)*0.2,
			"funding_rate":        nearPositive["funding_rate"] + float64(i%2)*0.02,
			"orderbook_imbalance": nearPositive["orderbook_imbalance"] + float64(i%2)*0.2,
		}
		appendRow("PULSEUSDT", 300+int64(i), scores, 0.005, "", "Type_B")
	}
	for i := 0; i < 8; i++ {
		scores := map[string]float64{
			"market":              nearPositive["market"] - float64(i%2)*0.005,
			"trend":               nearPositive["trend"] + float64(i%2)*0.010,
			"donchian_factor":     nearPositive["donchian_factor"] - float64(i%2)*0.008,
			"volume_spike":        nearPositive["volume_spike"] + float64(i%2)*0.010,
			"mtf_resonance":       nearPositive["mtf_resonance"] + float64(i%2)*0.008,
			"quant_netflow":       nearPositive["quant_netflow"] - float64(i%2)*0.008,
			"vol_utilization":     nearPositive["vol_utilization"] + float64(i%2)*0.2,
			"funding_rate":        nearPositive["funding_rate"] + float64(i%2)*0.02,
			"orderbook_imbalance": nearPositive["orderbook_imbalance"] + float64(i%2)*0.2,
		}
		appendRow("PULSEUSDT", 400+int64(i), scores, -0.001, "[REBEL_SON]", "Type_B")
	}

	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("seed radius risk rows: %v", err)
	}
}

func TestRealBacktestOpenPersistsGlobalPhysicalIdentity(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_identity.db")
	defer st.Close()

	fakeTrader := &fakeBacktestTrader{currentPrice: 100}
	at := &AutoTrader{
		id:                       "bt-test",
		store:                    st,
		trader:                   fakeTrader,
		exchange:                 "binance",
		exchangeID:               "acct-1",
		config:                   AutoTraderConfig{IsCrossMargin: true},
		positionFirstSeenTime:    make(map[string]int64),
		realBacktestHoldDuration: 15 * time.Minute,
	}

	actionRecord := &store.DecisionAction{
		Action:   "open_long",
		Symbol:   "BTCUSDT",
		Leverage: 10,
		RealBacktest: &store.RealBacktestDecisionMeta{
			Global:    &store.RealBacktestDimension{ExpectedValue: 0.02},
			SectorBin: &store.RealBacktestDimension{ExpectedValue: 0.03},
			Symbol:    &store.RealBacktestDimension{ExpectedValue: 0.05},
		},
	}
	decision := &kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 38,
	}

	if err := at.executeRealBacktestOpen(decision, actionRecord, 100); err != nil {
		t.Fatalf("execute real backtest open: %v", err)
	}

	if fakeTrader.openLongCalls != 1 {
		t.Fatalf("expected 1 open long call, got %d", fakeTrader.openLongCalls)
	}
	if fakeTrader.lastLeverage != 10 {
		t.Fatalf("expected leverage 10, got %d", fakeTrader.lastLeverage)
	}
	if fakeTrader.setMarginCalls != 1 {
		t.Fatalf("expected 1 margin mode call, got %d", fakeTrader.setMarginCalls)
	}
	if fakeTrader.lastMarginIsCross {
		t.Fatal("expected isolated margin mode to be forced")
	}
	if diff := math.Abs(fakeTrader.lastQuantity - 0.38); diff > 1e-12 {
		t.Fatalf("expected quantity 0.38 from 3.80 USDT margin * 10x notional, got %.4f", fakeTrader.lastQuantity)
	}
	if fakeTrader.stopLossCalls != 0 || fakeTrader.takeProfitCalls != 0 {
		t.Fatalf("expected no stop-loss/take-profit calls, got sl=%d tp=%d", fakeTrader.stopLossCalls, fakeTrader.takeProfitCalls)
	}

	openPositions, err := st.Position().GetOpenPositions(GlobalRealSniperTraderID)
	if err != nil {
		t.Fatalf("get global open positions: %v", err)
	}
	if len(openPositions) != 1 {
		t.Fatalf("expected 1 global open position, got %d", len(openPositions))
	}
	pos := openPositions[0]
	if pos.TraderID != GlobalRealSniperTraderID {
		t.Fatalf("expected trader_id %s, got %s", GlobalRealSniperTraderID, pos.TraderID)
	}
	if pos.ExchangeID != "acct-1" {
		t.Fatalf("expected exchange_id acct-1, got %s", pos.ExchangeID)
	}
	if pos.AutoCloseAt <= pos.EntryTime {
		t.Fatalf("expected auto_close_at after entry_time, got entry=%d auto_close=%d", pos.EntryTime, pos.AutoCloseAt)
	}

	orders, err := st.Order().GetTraderOrders(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get global orders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 global order, got %d", len(orders))
	}
	if orders[0].TraderID != GlobalRealSniperTraderID {
		t.Fatalf("expected global order trader_id, got %s", orders[0].TraderID)
	}

	record, err := st.RealTradeStats().GetByOrderID(GlobalRealSniperTraderID, "acct-1", "11")
	if err != nil {
		t.Fatalf("load real trade resonance record: %v", err)
	}
	if record == nil {
		t.Fatal("expected real trade resonance open record")
	}
	if record.EntrySymbolEV != 0.05 {
		t.Fatalf("expected symbol EV snapshot 0.05, got %.4f", record.EntrySymbolEV)
	}
	if record.HoldSampleCount != 1 {
		t.Fatalf("expected initial hold sample count 1, got %d", record.HoldSampleCount)
	}
}

func TestBuildRealBacktestDecisionMetaCapturesSmoothedDimensions(t *testing.T) {
	selection := &realBacktestSelection{
		sector:           "AI",
		logicScore:       77.4,
		positionUSD:      2500,
		entryAllowed:     true,
		entryBlockReason: "",
		resonance: kernel.ResonanceResult{
			Signal:   kernel.DirectionalResonanceLong,
			BinStart: 77,
			GlobalBin: &store.ScoreBinPerformance{
				BinStart:                 77,
				TradeCount:               48,
				ExpectedValueLong:        0.041,
				MedianExpectedValueLong:  0.032,
				ExpectedValueShort:       -0.017,
				MedianExpectedValueShort: -0.021,
				ProfitFactorLong:         1.8,
				ProfitFactorShort:        1.1,
			},
			SectorBin: &store.ScoreBinPerformance{
				BinStart:                 77,
				TradeCount:               21,
				ExpectedValueLong:        0.053,
				MedianExpectedValueLong:  0.044,
				ExpectedValueShort:       -0.014,
				MedianExpectedValueShort: -0.018,
				ProfitFactorLong:         2.2,
				ProfitFactorShort:        1.3,
			},
			SymbolBin: &store.ScoreBinPerformance{
				BinStart:                 77,
				TradeCount:               12,
				ExpectedValueLong:        0.061,
				MedianExpectedValueLong:  0.058,
				ExpectedValueShort:       -0.009,
				MedianExpectedValueShort: -0.013,
				ProfitFactorLong:         2.6,
				ProfitFactorShort:        1.5,
			},
		},
	}

	meta := buildRealBacktestDecisionMeta(selection)
	if meta == nil {
		t.Fatalf("expected real backtest meta")
	}
	if meta.BinCenter != 77 {
		t.Fatalf("expected bin center 77, got %d", meta.BinCenter)
	}
	if meta.SmoothingHalfWidth != 0 {
		t.Fatalf("expected raw bin half width 0, got %.2f", meta.SmoothingHalfWidth)
	}
	if meta.Global == nil || meta.Global.ExpectedValue != 0.041 {
		t.Fatalf("expected global EV copy, got %+v", meta.Global)
	}
	if meta.Global == nil || meta.Global.ExpectedValueShort != -0.017 {
		t.Fatalf("expected global opposite EV copy, got %+v", meta.Global)
	}
	if meta.SectorBin == nil || meta.SectorBin.MedianExpectedValue != 0.044 {
		t.Fatalf("expected sector median EV copy, got %+v", meta.SectorBin)
	}
	if meta.Symbol == nil || meta.Symbol.ProfitFactor != 2.6 {
		t.Fatalf("expected symbol PF copy, got %+v", meta.Symbol)
	}
	if meta.Symbol == nil || meta.Symbol.MedianExpectedValueShort != -0.013 {
		t.Fatalf("expected symbol opposite median EV copy, got %+v", meta.Symbol)
	}
	if meta.EntryAllowed == nil || !*meta.EntryAllowed {
		t.Fatal("expected entry allowed flag to copy into decision meta")
	}
}

func TestRealBacktestSizingInterpolatesFromAvgEV(t *testing.T) {
	avgEV := realBacktestAverageEV(0.0018, 0.0019, 0.0020, nil)
	if diff := math.Abs(avgEV - 0.0019); diff > 1e-12 {
		t.Fatalf("expected avg EV 0.0019, got %.10f", avgEV)
	}

	marginUsageRatio := realBacktestMarginUsageRatio(avgEV, 0.0010, 0.0025)
	if diff := math.Abs(marginUsageRatio - 0.76); diff > 1e-12 {
		t.Fatalf("expected margin usage ratio 0.76, got %.10f", marginUsageRatio)
	}

	requiredMargin := realBacktestRequiredMargin(5.0, marginUsageRatio)
	if diff := math.Abs(requiredMargin - 3.8); diff > 1e-12 {
		t.Fatalf("expected required margin 3.8, got %.10f", requiredMargin)
	}

	positionUSD := realBacktestRequiredPositionUSD(requiredMargin)
	if diff := math.Abs(positionUSD - 38.0); diff > 1e-12 {
		t.Fatalf("expected notional 38.0, got %.10f", positionUSD)
	}
}

func TestRealBacktestAverageEVUsesLiveAttributionWeights(t *testing.T) {
	avgEV := realBacktestAverageEV(0.0070, 0.0010, -0.0090, map[string]float64{
		"global": 0.012,
		"sector": 0.900,
		"symbol": 0.087,
	})
	if diff := math.Abs(avgEV - 0.0002012012012); diff > 1e-9 {
		t.Fatalf("expected weighted avg EV 0.0002012012, got %.10f", avgEV)
	}
}

func TestRealBacktestDimensionBucketPrefersSectorWeight(t *testing.T) {
	selection := &realBacktestSelection{
		globalEV: 0.0020,
		sectorEV: 0.0015,
		symbolEV: 0.0018,
		attributionWeights: map[string]float64{
			"global": 0.012,
			"sector": 0.900,
			"symbol": 0.087,
		},
	}

	if got := realBacktestDimensionBucket(selection); got != "SECTOR" {
		t.Fatalf("expected sector to dominate weighted bucket selection, got %s", got)
	}
}

func TestEvaluateRealBacktestSizingRejectsLowEVAndReserveBreach(t *testing.T) {
	cfg := store.RealBacktestSystemConfig{
		MaxMarginPerTrade: 5.0,
		ReserveMargin:     6.0,
		MinEVThreshold:    0.0010,
		MaxEVThreshold:    0.0025,
	}

	_, _, meetsThreshold, reserveBreached := evaluateRealBacktestSizing(0.0009, cfg, 100.0)
	if meetsThreshold {
		t.Fatal("expected low EV to fail threshold check")
	}
	if reserveBreached {
		t.Fatal("expected low EV rejection to happen before reserve check")
	}

	usageRatio, requiredMargin, meetsThreshold, reserveBreached := evaluateRealBacktestSizing(0.0019, cfg, 9.0)
	if !meetsThreshold {
		t.Fatal("expected 0.19% EV to pass threshold")
	}
	if !reserveBreached {
		t.Fatal("expected 9 USDT free balance to breach reserve margin")
	}
	if diff := math.Abs(usageRatio - 0.76); diff > 1e-12 {
		t.Fatalf("expected usage ratio 0.76, got %.10f", usageRatio)
	}
	if diff := math.Abs(requiredMargin - 3.8); diff > 1e-12 {
		t.Fatalf("expected required margin 3.8, got %.10f", requiredMargin)
	}
}

func TestRealBacktestGuardRejectsWeakFactorTriad(t *testing.T) {
	factors := realBacktestGuardFactors(0.0018, 0.0050, 0.0027)
	ok, reason, minFactor := kernel.ValidateResonanceFactors(factors, 45)
	if ok {
		t.Fatal("expected weak factor triad to be rejected")
	}
	if reason != "Logic Incoherence" {
		t.Fatalf("expected logic incoherence, got %s", reason)
	}
	if diff := math.Abs(minFactor - 18); diff > 1e-12 {
		t.Fatalf("expected min factor 18, got %.4f", minFactor)
	}

	score := kernel.CalculateResonanceScore(factors, nil, 0.1)
	if score >= 45 {
		t.Fatalf("expected resonance score to fall below floor, got %.4f", score)
	}
}

func TestRealBacktestCandidateHighNoiseRadiusRejectsEntryFloor(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_rnn_high_noise.db")
	defer st.Close()

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
	seedRadiusRiskFloorRows(t, st, "wolf-trader", baseTime)

	cache := NewPerformanceMatrixCache(st, time.Minute)
	fakeTrader := &fakeBacktestTrader{currentPrice: 100}
	at := &AutoTrader{
		id:                    "wolf-trader",
		store:                 st,
		trader:                fakeTrader,
		performanceCache:      cache,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	var logBuffer bytes.Buffer
	oldOutput := logger.Log.Out
	logger.Log.SetOutput(&logBuffer)
	t.Cleanup(func() {
		logger.Log.SetOutput(oldOutput)
	})

	logicScore := 90.0
	ctx := &kernel.Context{
		Account: kernel.AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		CandidateCoins: []kernel.CandidateCoin{
			{
				Symbol:     "PULSEUSDT",
				LogicScore: &logicScore,
			},
		},
		MarketDataMap: map[string]*market.Data{
			"PULSEUSDT": buildTypeBMarketData(),
		},
	}

	riskConfig := store.RealBacktestSystemConfig{
		MaxMarginPerTrade: 5.0,
		ReserveMargin:     0,
		MinEVThreshold:    0.001,
		MaxEVThreshold:    0.010,
	}
	guardConfig := store.ResonanceGuardSystemConfig{
		AdaptiveEntryFloor:  9.9,
		AdaptiveEntryLambda: 0.5,
	}

	assessment, err := at.assessRealBacktestCandidate(ctx, ctx.CandidateCoins[0], riskConfig, guardConfig)
	if err != nil {
		t.Fatalf("assess real backtest candidate: %v", err)
	}
	if assessment != nil {
		t.Fatalf("expected high-noise radius zone to block entry, got %+v", assessment.selection)
	}
	if fakeTrader.openLongCalls != 0 || fakeTrader.openShortCalls != 0 {
		t.Fatalf("expected no order calls, got long=%d short=%d", fakeTrader.openLongCalls, fakeTrader.openShortCalls)
	}
	logs := logBuffer.String()
	if !strings.Contains(logs, "原始EV") || !strings.Contains(logs, "高噪诱多区") || !strings.Contains(logs, "折价后EV") {
		t.Fatalf("expected risk-adjusted rejection log, got %q", logs)
	}
}

func TestRealBacktestHighSlopePulseMatchesTypeBArchetype(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_type_b.db")
	defer st.Close()

	seedArchetypeBacktestRows(t, st, "pulse-trader", time.Now().UTC().Add(-2*time.Hour).UnixMilli())

	at := &AutoTrader{
		id:                    "pulse-trader",
		store:                 st,
		performanceCache:      NewPerformanceMatrixCache(st, time.Minute),
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	logicScore := 90.0
	ctx := &kernel.Context{
		Account: kernel.AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		CandidateCoins: []kernel.CandidateCoin{
			{Symbol: "PULSEUSDT", LogicScore: &logicScore},
		},
		MarketDataMap: map[string]*market.Data{
			"PULSEUSDT": buildTypeBMarketData(),
		},
	}

	assessment, err := at.assessRealBacktestCandidate(ctx, ctx.CandidateCoins[0], store.RealBacktestSystemConfig{
		MaxMarginPerTrade: 5.0,
		ReserveMargin:     0,
		MinEVThreshold:    0.001,
		MaxEVThreshold:    0.010,
	}, store.ResonanceGuardSystemConfig{
		AdaptiveEntryFloor:  45,
		AdaptiveEntryLambda: 0.5,
	})
	if err != nil {
		t.Fatalf("assess type_b candidate: %v", err)
	}
	if assessment == nil || assessment.selection == nil {
		t.Fatal("expected Type_B pulse candidate to pass assessment")
	}
	if assessment.selection.archetypeID != "Type_B" {
		t.Fatalf("expected Type_B archetype, got %s", assessment.selection.archetypeID)
	}
	if assessment.selection.mahalanobisDistance > kernel.DefaultMahalanobisThreshold {
		t.Fatalf("expected distance <= %.1f, got %.4f", kernel.DefaultMahalanobisThreshold, assessment.selection.mahalanobisDistance)
	}
	if !assessment.selection.entryAllowed {
		t.Fatalf("expected entry allowed, block reason=%s", assessment.selection.entryBlockReason)
	}
}

func TestRealBacktestUnknownMutationPreservesOutlierForIncubation(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_outlier.db")
	defer st.Close()

	seedArchetypeBacktestRows(t, st, "mutant-trader", time.Now().UTC().Add(-2*time.Hour).UnixMilli())

	nowMs := time.Now().UTC().UnixMilli()
	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{
		{
			TraderID:     "mutant-trader",
			DecisionTime: nowMs,
			Symbol:       "PULSEUSDT",
			Sector:       "AI",
			PriceT0:      100,
			HeatScore:    90,
			CreatedAt:    nowMs,
			UpdatedAt:    nowMs,
		},
	}); err != nil {
		t.Fatalf("seed pending outlier row: %v", err)
	}

	var logBuffer bytes.Buffer
	oldOutput := logger.Log.Out
	logger.Log.SetOutput(&logBuffer)
	t.Cleanup(func() {
		logger.Log.SetOutput(oldOutput)
	})

	at := &AutoTrader{
		id:                    "mutant-trader",
		store:                 st,
		performanceCache:      NewPerformanceMatrixCache(st, time.Minute),
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	logicScore := 90.0
	ctx := &kernel.Context{
		Account: kernel.AccountInfo{
			TotalEquity:      1000,
			AvailableBalance: 1000,
		},
		CandidateCoins: []kernel.CandidateCoin{
			{Symbol: "PULSEUSDT", LogicScore: &logicScore},
		},
		MarketDataMap: map[string]*market.Data{
			"PULSEUSDT": buildMutantMarketData(),
		},
	}

	assessment, err := at.assessRealBacktestCandidate(ctx, ctx.CandidateCoins[0], store.RealBacktestSystemConfig{
		MaxMarginPerTrade: 5.0,
		ReserveMargin:     0,
		MinEVThreshold:    0.001,
		MaxEVThreshold:    0.010,
	}, store.ResonanceGuardSystemConfig{
		AdaptiveEntryFloor:  45,
		AdaptiveEntryLambda: 0.5,
	})
	if err != nil {
		t.Fatalf("assess mutant candidate: %v", err)
	}
	if assessment != nil {
		t.Fatalf("expected outlier to be blocked, got %+v", assessment.selection)
	}

	latest, err := st.Shadow().GetLatestBySymbol("mutant-trader", "PULSEUSDT", false)
	if err != nil {
		t.Fatalf("load latest outlier row: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest outlier shadow snapshot")
	}
	if !latest.Incubating {
		t.Fatalf("expected latest shadow snapshot to be incubating, got %+v", latest)
	}
	if latest.IncubationPromoted {
		t.Fatalf("expected outlier to stay unpromoted, got %+v", latest)
	}
	if !strings.Contains(logBuffer.String(), "Outlier preserved for incubation") {
		t.Fatalf("expected incubation log, got %q", logBuffer.String())
	}
}

func TestRealBacktestCloseUsesGlobalPhysicalIdentity(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_close.db")
	defer st.Close()

	fakeTrader := &fakeBacktestTrader{currentPrice: 100}
	at := &AutoTrader{
		id:                       "bt-test",
		store:                    st,
		trader:                   fakeTrader,
		exchange:                 "binance",
		exchangeID:               "acct-1",
		config:                   AutoTraderConfig{IsCrossMargin: false},
		positionFirstSeenTime:    make(map[string]int64),
		realBacktestHoldDuration: 15 * time.Minute,
	}

	openRecord := &store.DecisionAction{
		Action:   "open_long",
		Symbol:   "BTCUSDT",
		Leverage: 10,
		RealBacktest: &store.RealBacktestDecisionMeta{
			Global:    &store.RealBacktestDimension{ExpectedValue: 0.02},
			SectorBin: &store.RealBacktestDimension{ExpectedValue: 0.03},
			Symbol:    &store.RealBacktestDimension{ExpectedValue: 0.05},
		},
	}
	decision := &kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_long",
		Leverage:        10,
		PositionSizeUSD: 2500,
	}
	if err := at.executeRealBacktestOpen(decision, openRecord, 100); err != nil {
		t.Fatalf("execute real backtest open: %v", err)
	}

	fakeTrader.currentPrice = 105
	closeRecord := &store.DecisionAction{
		Action:        "close_long",
		Symbol:        "BTCUSDT",
		Timestamp:     time.Now().UTC(),
		Reasoning:     buildRealBacktestTimedExitReason(at.getRealBacktestHoldDuration()),
		ExecutionMode: "real_backtest",
	}
	if err := at.executeRealBacktestClose("BTCUSDT", "close_long", closeRecord); err != nil {
		t.Fatalf("execute real backtest close: %v", err)
	}

	if fakeTrader.closeLongCalls != 1 {
		t.Fatalf("expected 1 close long call, got %d", fakeTrader.closeLongCalls)
	}

	closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get global closed positions: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("expected 1 global closed position, got %d", len(closed))
	}
	if closed[0].TraderID != GlobalRealSniperTraderID {
		t.Fatalf("expected global closed position trader_id, got %s", closed[0].TraderID)
	}
	if closed[0].CloseReason == "" {
		t.Fatal("expected close reason to be persisted")
	}

	record, err := st.RealTradeStats().GetByOrderID(GlobalRealSniperTraderID, "acct-1", "11")
	if err != nil {
		t.Fatalf("load real trade resonance record after close: %v", err)
	}
	if record == nil {
		t.Fatal("expected finalized real trade resonance record")
	}
	if record.Status != store.RealTradeResonanceStatusClosed {
		t.Fatalf("expected closed resonance record, got %s", record.Status)
	}
	if record.FinalPnL <= 0 {
		t.Fatalf("expected positive final pnl, got %.4f", record.FinalPnL)
	}
}

func TestRealBacktestOpenSchedulesTimedExit(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_timed_exit.db")
	defer st.Close()

	fakeTrader := &fakeBacktestTrader{currentPrice: 100}
	at := &AutoTrader{
		id:                       "bt-test",
		store:                    st,
		trader:                   fakeTrader,
		exchange:                 "binance",
		exchangeID:               "acct-1",
		config:                   AutoTraderConfig{IsCrossMargin: false},
		positionFirstSeenTime:    make(map[string]int64),
		realBacktestHoldDuration: 50 * time.Millisecond,
	}

	actionRecord := &store.DecisionAction{
		Action:   "open_short",
		Symbol:   "BTCUSDT",
		Leverage: 10,
		RealBacktest: &store.RealBacktestDecisionMeta{
			Global:    &store.RealBacktestDimension{ExpectedValue: 0.02},
			SectorBin: &store.RealBacktestDimension{ExpectedValue: 0.03},
			Symbol:    &store.RealBacktestDimension{ExpectedValue: 0.05},
		},
	}
	decision := &kernel.Decision{
		Symbol:          "BTCUSDT",
		Action:          "open_short",
		Leverage:        10,
		PositionSizeUSD: 38,
	}

	if err := at.executeRealBacktestOpen(decision, actionRecord, 100); err != nil {
		t.Fatalf("execute real backtest open: %v", err)
	}

	waitForCondition(t, 800*time.Millisecond, 10*time.Millisecond, func() bool {
		closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
		return err == nil && len(closed) == 1
	})

	closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get global closed positions: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(closed))
	}
	if fakeTrader.closeShortCalls != 1 {
		t.Fatalf("expected 1 close short call, got %d", fakeTrader.closeShortCalls)
	}

	deltaMs := closed[0].ExitTime - closed[0].AutoCloseAt
	if deltaMs < 0 {
		deltaMs = -deltaMs
	}
	if deltaMs > 250 {
		t.Fatalf("expected close close to auto_close_at, delta=%dms exit=%d auto_close=%d", deltaMs, closed[0].ExitTime, closed[0].AutoCloseAt)
	}
}

func TestRealBacktestTimedCloseIsIdempotent(t *testing.T) {
	st := setupBacktestStore(t, "nofx_backtest_executor_idempotent.db")
	defer st.Close()

	now := time.Now().UTC()
	entryTime := now.Add(-20 * time.Minute).UnixMilli()
	autoCloseAt := now.Add(-1 * time.Minute).UnixMilli()

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
		closeShortBlock:  make(chan struct{}),
	}
	at := &AutoTrader{
		id:                    "bt-test",
		store:                 st,
		trader:                fakeTrader,
		exchange:              "binance",
		exchangeID:            "acct-1",
		positionFirstSeenTime: make(map[string]int64),
	}

	openPos, err := st.Position().GetOpenPositionBySymbol(GlobalRealSniperTraderID, "BTCUSDT", "SHORT")
	if err != nil {
		t.Fatalf("load open position: %v", err)
	}
	if openPos == nil {
		t.Fatal("expected seeded open position")
	}

	errCh1 := make(chan error, 1)
	errCh2 := make(chan error, 1)
	go func() {
		errCh1 <- at.forceCloseExpiredRealBacktestPositionByID(openPos.ID)
	}()

	time.Sleep(20 * time.Millisecond)
	go func() {
		errCh2 <- at.forceCloseExpiredRealBacktestPositionByID(openPos.ID)
	}()

	close(fakeTrader.closeShortBlock)

	if err := <-errCh1; err != nil {
		t.Fatalf("first close attempt failed: %v", err)
	}
	if err := <-errCh2; err != nil {
		t.Fatalf("second close attempt failed: %v", err)
	}

	waitForCondition(t, 800*time.Millisecond, 10*time.Millisecond, func() bool {
		closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
		return err == nil && len(closed) == 1
	})

	if fakeTrader.closeShortCalls != 1 {
		t.Fatalf("expected one close short call, got %d", fakeTrader.closeShortCalls)
	}

	closed, err := st.Position().GetClosedPositions(GlobalRealSniperTraderID, 10)
	if err != nil {
		t.Fatalf("get closed positions: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("expected one closed position, got %d", len(closed))
	}
}
