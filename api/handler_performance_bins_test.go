package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
	tradercache "nofx/trader"

	"github.com/gin-gonic/gin"
)

func TestLoadPerformanceBinsFallbacksToGlobalPool(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_api_performance_bins_pool_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{
		{
			TraderID:     "legacy-trader",
			DecisionTime: baseTime + 1,
			Symbol:       "POOLUSDT",
			Sector:       "AI",
			HeatScore:    71.0,
			PriceT0:      100,
			PriceT1:      108,
			ReturnPct:    0.08,
			Filled:       true,
			FilledAt:     baseTime + 900000,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900000,
		},
	}); err != nil {
		t.Fatalf("create shadow snapshot: %v", err)
	}

	srv := &Server{store: st}
	rows, err := srv.loadPerformanceBins("fresh-trader", "global", "", "", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, false)
	if err != nil {
		t.Fatalf("load performance bins: %v", err)
	}
	if !hasPerformanceBins(rows) {
		t.Fatalf("expected global pool fallback rows, got %+v", rows)
	}
}

func TestLoadPerformanceBinsBackcastFallbacksToGlobalRawFactors(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_api_performance_bins_backcast_pool_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	rawFactors := store.ShadowRawFactors{
		Scores: map[string]float64{
			"market":          0,
			"trend":           2,
			"donchian_factor": 2,
			"trend_group":     2,
		},
		Available: map[string]bool{
			"market":          true,
			"trend":           true,
			"donchian_factor": true,
			"trend_group":     true,
		},
	}.MarshalText()

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{
		{
			TraderID:     "deleted-trader",
			DecisionTime: baseTime + 1,
			Symbol:       "POOLBACKUSDT",
			Sector:       "AI",
			HeatScore:    25.0,
			PriceT0:      100,
			PriceT1:      130,
			ReturnPct:    0.30,
			Filled:       true,
			FilledAt:     baseTime + 900000,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900000,
			RawFactors:   rawFactors,
		},
	}); err != nil {
		t.Fatalf("create shadow snapshot: %v", err)
	}

	srv := &Server{store: st}
	rows, err := srv.loadPerformanceBins("fresh-trader", "global", "", "", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
	if err != nil {
		t.Fatalf("load backcast performance bins: %v", err)
	}
	if !hasPerformanceBins(rows) {
		t.Fatalf("expected non-empty backcast rows from global raw factors, got %+v", rows)
	}
}

func TestLoadPerformanceBinsBackcastResonanceFilterToggle(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_api_performance_bins_backcast_filter_toggle_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	market.SetAdaptiveWeightStore(st)
	defer market.SetAdaptiveWeightStore(nil)
	market.InvalidateAdaptiveWeightScope("")

	baseTime := time.Now().UTC().Add(-3 * time.Hour).UnixMilli()
	makeRawFactors := func(progress float64) string {
		return store.ShadowRawFactors{
			Scores: map[string]float64{
				"market":            20 + progress*60,
				"trend":             80 - progress*60,
				"volume_spike":      45,
				"quant_oi":          45,
				"quant_imbalance":   45,
				"quant_netflow":     45,
				"social_rank":       45,
				"social_upvote":     45,
				"onchain_ratio":     45,
				"onchain_buy_ratio": 45,
			},
			Available: map[string]bool{
				"market":            true,
				"trend":             true,
				"volume_spike":      true,
				"quant_oi":          true,
				"quant_imbalance":   true,
				"quant_netflow":     true,
				"social_rank":       true,
				"social_upvote":     true,
				"onchain_ratio":     true,
				"onchain_buy_ratio": true,
			},
		}.MarshalText()
	}

	rows := make([]*store.ShadowSnapshot, 0, 21)
	for i := 0; i < 20; i++ {
		progress := float64(i) / 19.0
		returnPct := 0.02 + progress*0.06
		rows = append(rows, &store.ShadowSnapshot{
			TraderID:          "resonance-trader",
			DecisionTime:      baseTime + int64(i)*60000,
			Symbol:            "RESUSDT",
			Sector:            "AI",
			HeatScore:         50 + progress*20,
			PriceT0:           100,
			PriceT1:           100 * (1 + returnPct),
			ReturnPct:         returnPct,
			Filled:            true,
			FilledAt:          baseTime + int64(i)*60000 + 900000,
			CreatedAt:         baseTime + int64(i)*60000,
			UpdatedAt:         baseTime + int64(i)*60000 + 900000,
			RawFactors:        makeRawFactors(progress),
			VolUtilization:    0.12,
			FundingRate:       0.0004,
			QuantImbalanceRaw: 0.09,
		})
	}
	outlierProgress := 0.5
	outlierReturnPct := 0.02 + outlierProgress*0.06
	rows = append(rows, &store.ShadowSnapshot{
		TraderID:          "resonance-trader",
		DecisionTime:      baseTime + 20*60000,
		Symbol:            "RESUSDT",
		Sector:            "AI",
		HeatScore:         50 + outlierProgress*20,
		PriceT0:           100,
		PriceT1:           100 * (1 + outlierReturnPct),
		ReturnPct:         outlierReturnPct,
		Filled:            true,
		FilledAt:          baseTime + 20*60000 + 900000,
		CreatedAt:         baseTime + 20*60000,
		UpdatedAt:         baseTime + 20*60000 + 900000,
		RawFactors:        makeRawFactors(outlierProgress),
		VolUtilization:    2.5,
		FundingRate:       0.012,
		QuantImbalanceRaw: 0.92,
	})

	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create resonance filter rows: %v", err)
	}

	srv := &Server{store: st}
	filteredRows, err := srv.loadPerformanceBins("resonance-trader", "global", "", "", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true, true)
	if err != nil {
		t.Fatalf("load filtered backcast bins: %v", err)
	}
	rawRows, err := srv.loadPerformanceBins("resonance-trader", "global", "", "", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true, false)
	if err != nil {
		t.Fatalf("load raw backcast bins: %v", err)
	}

	filteredTradeCount := sumPerformanceTradeCount(filteredRows)
	rawTradeCount := sumPerformanceTradeCount(rawRows)
	if filteredTradeCount >= rawTradeCount {
		t.Fatalf("expected resonance filter to remove at least one sample, filtered=%d raw=%d", filteredTradeCount, rawTradeCount)
	}
	if rawTradeCount-filteredTradeCount != 1 {
		t.Fatalf("expected exactly one anomalous sample to be removed, filtered=%d raw=%d", filteredTradeCount, rawTradeCount)
	}
}

func TestHandlePerformanceBinsBackcastHTTPSharesGlobalWeightSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join("/tmp", "nofx_api_performance_bins_backcast_http_signature_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	market.SetAdaptiveWeightStore(st)
	defer market.SetAdaptiveWeightStore(nil)
	market.InvalidateAdaptiveWeightScope("")

	const userID = "user-http"
	for _, traderID := range []string{"api-trader-a", "api-trader-b"} {
		if err := st.Trader().Create(&store.Trader{
			ID:                  traderID,
			UserID:              userID,
			Name:                traderID,
			AIModelID:           "ai",
			ExchangeID:          "exchange",
			InitialBalance:      1000,
			ScanIntervalMinutes: 3,
		}); err != nil {
			t.Fatalf("create trader %s: %v", traderID, err)
		}
	}

	seedHTTPAdaptiveWeightHistory(t, st, "api-trader-a", false)
	seedHTTPAdaptiveWeightHistory(t, st, "api-trader-b", true)
	seedHTTPGlobalBackcastRawRows(t, st)

	srv := &Server{
		store:            st,
		performanceCache: tradercache.NewPerformanceMatrixCache(st, time.Minute),
	}

	rowsA := performBackcastBinsHTTPRequest(t, srv, userID, "api-trader-a")
	rowsB := performBackcastBinsHTTPRequest(t, srv, userID, "api-trader-b")

	if !hasPerformanceBins(rowsA) {
		t.Fatalf("expected non-empty HTTP backcast rows for trader A, got %+v", rowsA)
	}
	if !hasPerformanceBins(rowsB) {
		t.Fatalf("expected non-empty HTTP backcast rows for trader B, got %+v", rowsB)
	}
	if !reflect.DeepEqual(rowsA, rowsB) {
		t.Fatalf("expected identical HTTP backcast payloads from shared global weights, got A=%+v B=%+v", rowsA, rowsB)
	}
}

func performBackcastBinsHTTPRequest(t *testing.T, srv *Server, userID, traderID string) []*store.ScoreBinPerformance {
	t.Helper()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodGet, "/api/data-lab/performance-bins/backcast?trader_id="+traderID+"&scope=global", nil)
	context.Request = request
	context.Set("user_id", userID)

	srv.handlePerformanceBinsBackcast(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200 for trader %s, got %d body=%s", traderID, recorder.Code, recorder.Body.String())
	}

	var rows []*store.ScoreBinPerformance
	if err := json.Unmarshal(recorder.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode backcast rows for trader %s: %v body=%s", traderID, err, recorder.Body.String())
	}
	return rows
}

func seedHTTPAdaptiveWeightHistory(t *testing.T, st *store.Store, traderID string, marketLeads bool) {
	t.Helper()

	baseTime := time.Now().UTC().Add(-4 * time.Hour)
	rows := make([]*store.ShadowSnapshot, 0, 40)
	for i := 0; i < 40; i++ {
		progress := float64(i) / 39.0
		returnPct := -0.05 + progress*0.10

		marketFactor := 80 - progress*60
		trendFactor := 20 + progress*60
		if marketLeads {
			marketFactor = 20 + progress*60
			trendFactor = 80 - progress*60
		}

		rows = append(rows, &store.ShadowSnapshot{
			TraderID:           traderID,
			DecisionTime:       baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			Symbol:             traderID + "-HTTP-" + string(rune('A'+i%26)),
			Sector:             "AI",
			ActionTaken:        i % 2,
			PriceT0:            100,
			HeatScore:          50 + progress*20,
			MarketFactor:       marketFactor,
			TrendFactor:        trendFactor,
			DonchianFactor:     45,
			VolumeSpikeFactor:  45,
			MTFResonanceFactor: 45,
			QuantFactor:        45,
			QuantOIRaw:         1,
			QuantImbalanceRaw:  1,
			QuantNetflowRaw:    1,
			SocialFactor:       45,
			SocialRankRaw:      1,
			SocialUpvoteRaw:    1,
			OnChainFactor:      45,
			OnChainRatioRaw:    1,
			OnChainBuyRaw:      1,
			Filled:             true,
			PriceT1:            100 * (1 + returnPct),
			ReturnPct:          returnPct,
			FilledAt:           baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
			CreatedAt:          baseTime.Add(time.Duration(i) * time.Minute).UnixMilli(),
			UpdatedAt:          baseTime.Add(time.Duration(i+15) * time.Minute).UnixMilli(),
		})
	}

	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create HTTP adaptive weight history for %s: %v", traderID, err)
	}
}

func seedHTTPGlobalBackcastRawRows(t *testing.T, st *store.Store) {
	t.Helper()

	baseTime := time.Now().UTC().Add(-2 * time.Hour)
	rows := []*store.ShadowSnapshot{
		{
			TraderID:     "deleted-trader",
			DecisionTime: baseTime.Add(1 * time.Minute).UnixMilli(),
			Symbol:       "HTTP-POOL-TREND",
			Sector:       "AI",
			HeatScore:    50,
			PriceT0:      100,
			PriceT1:      120,
			ReturnPct:    0.20,
			Filled:       true,
			FilledAt:     baseTime.Add(16 * time.Minute).UnixMilli(),
			CreatedAt:    baseTime.Add(1 * time.Minute).UnixMilli(),
			UpdatedAt:    baseTime.Add(16 * time.Minute).UnixMilli(),
			RawFactors: store.ShadowRawFactors{
				Scores: map[string]float64{
					"trend":           2.2,
					"trend_group":     2.2,
					"donchian_factor": 0,
				},
				Available: map[string]bool{
					"trend":           true,
					"trend_group":     true,
					"donchian_factor": false,
				},
			}.MarshalText(),
		},
		{
			TraderID:     "deleted-trader",
			DecisionTime: baseTime.Add(2 * time.Minute).UnixMilli(),
			Symbol:       "HTTP-POOL-MARKET",
			Sector:       "AI",
			HeatScore:    50,
			PriceT0:      100,
			PriceT1:      90,
			ReturnPct:    -0.10,
			Filled:       true,
			FilledAt:     baseTime.Add(17 * time.Minute).UnixMilli(),
			CreatedAt:    baseTime.Add(2 * time.Minute).UnixMilli(),
			UpdatedAt:    baseTime.Add(17 * time.Minute).UnixMilli(),
			RawFactors: store.ShadowRawFactors{
				Scores: map[string]float64{
					"market": -2.2,
				},
				Available: map[string]bool{
					"market": true,
				},
			}.MarshalText(),
		},
	}

	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create HTTP pooled raw-factor rows: %v", err)
	}
}

func sumPerformanceTradeCount(rows []*store.ScoreBinPerformance) int {
	total := 0
	for _, row := range rows {
		if row == nil {
			continue
		}
		total += row.TradeCount
	}
	return total
}
