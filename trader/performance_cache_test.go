package trader

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"nofx/market"
	"nofx/store"
)

func TestPerformanceMatrixCacheRespectsTTL(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_performance_cache_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
	rows := []*store.ShadowSnapshot{
		{
			TraderID:     "cache-trader",
			DecisionTime: baseTime + 1,
			Symbol:       "TESTUSDT",
			Sector:       "AI",
			HeatScore:    72.0,
			PriceT0:      100,
			PriceT1:      110,
			ReturnPct:    0.10,
			Filled:       true,
			FilledAt:     baseTime + 900000,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900000,
		},
		{
			TraderID:     "cache-trader",
			DecisionTime: baseTime + 2,
			Symbol:       "TESTUSDT",
			Sector:       "AI",
			HeatScore:    74.0,
			PriceT0:      100,
			PriceT1:      96,
			ReturnPct:    -0.04,
			Filled:       true,
			FilledAt:     baseTime + 900001,
			CreatedAt:    baseTime + 2,
			UpdatedAt:    baseTime + 900001,
		},
	}
	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	cache := newPerformanceMatrixCacheWithTTLs(st, 25*time.Millisecond, 25*time.Millisecond)
	first, err := cache.GetMatrices("cache-trader", "AI", "TESTUSDT")
	if err != nil {
		t.Fatalf("get matrices first: %v", err)
	}
	if len(first.Global) != 3 {
		t.Fatalf("expected 3 smoothed global points, got %+v", first.Global)
	}
	if first.Global[0].BinStart != 72 || first.Global[2].BinStart != 74 {
		t.Fatalf("unexpected first global bins: %+v", first.Global)
	}

	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{
		{
			TraderID:     "cache-trader",
			DecisionTime: baseTime + 3,
			Symbol:       "TESTUSDT",
			Sector:       "AI",
			HeatScore:    90.0,
			PriceT0:      100,
			PriceT1:      101,
			ReturnPct:    0.01,
			Filled:       true,
			FilledAt:     baseTime + 900002,
			CreatedAt:    baseTime + 3,
			UpdatedAt:    baseTime + 900002,
		},
	}); err != nil {
		t.Fatalf("create second batch: %v", err)
	}

	cached, err := cache.GetMatrices("cache-trader", "AI", "TESTUSDT")
	if err != nil {
		t.Fatalf("get matrices cached: %v", err)
	}
	if len(cached.Global) != 3 {
		t.Fatalf("expected cached global bins to remain unchanged, got %+v", cached.Global)
	}
	for _, bin := range cached.Global {
		if bin.BinStart >= 88 {
			t.Fatalf("expected cached bins to exclude the new 90-score window before ttl expiry, got %+v", cached.Global)
		}
	}

	time.Sleep(40 * time.Millisecond)

	refreshed, err := cache.GetMatrices("cache-trader", "AI", "TESTUSDT")
	if err != nil {
		t.Fatalf("get matrices refreshed: %v", err)
	}
	if len(refreshed.Global) <= len(first.Global) {
		t.Fatalf("expected refreshed global bins to expand after inserting the new 90-score sample, got %+v", refreshed.Global)
	}
	foundNinety := false
	for _, bin := range refreshed.Global {
		if bin.BinStart == 90 {
			foundNinety = true
			break
		}
	}
	if !foundNinety {
		t.Fatalf("expected refreshed smoothed bins to include score 90, got %+v", refreshed.Global)
	}
}

func TestPerformanceMatrixCacheBackcastRebinsUsingRawFactors(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_performance_backcast_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
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

	rows := []*store.ShadowSnapshot{
		{
			TraderID:     "cache-backcast",
			DecisionTime: baseTime + 1,
			Symbol:       "TESTUSDT",
			Sector:       "AI",
			HeatScore:    30,
			PriceT0:      100,
			PriceT1:      130,
			ReturnPct:    0.30,
			Filled:       true,
			FilledAt:     baseTime + 900000,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900000,
			RawFactors:   rawFactors,
		},
	}
	if err := st.Shadow().CreateBatch(rows); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	cache := newPerformanceMatrixCacheWithTTLs(st, time.Minute, time.Minute)

	snapshotMatrices, err := cache.GetMatrices("cache-backcast", "AI", "TESTUSDT")
	if err != nil {
		t.Fatalf("get snapshot matrices: %v", err)
	}
	if len(snapshotMatrices.Global) != 1 || snapshotMatrices.Global[0].BinStart != 30 {
		t.Fatalf("expected snapshot bin 30, got %+v", snapshotMatrices.Global)
	}

	backcastMatrices, err := cache.GetBackcastMatrices("cache-backcast", "AI", "TESTUSDT")
	if err != nil {
		t.Fatalf("get backcast matrices: %v", err)
	}
	if len(backcastMatrices.Global) == 0 {
		t.Fatalf("expected at least one backcast point, got %+v", backcastMatrices.Global)
	}
	for _, bin := range backcastMatrices.Global {
		if bin.BinStart == 30 {
			t.Fatalf("expected backcast to move the stale snapshot score out of bin 30, got %+v", backcastMatrices.Global)
		}
	}
}

func TestPerformanceMatrixCacheGlobalPoolKeyAndCrossTraderReuse(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_performance_global_pool_test.db")
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
			HeatScore:    66.0,
			PriceT0:      100,
			PriceT1:      104,
			ReturnPct:    0.04,
			Filled:       true,
			FilledAt:     baseTime + 900000,
			CreatedAt:    baseTime + 1,
			UpdatedAt:    baseTime + 900000,
		},
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	cache := newPerformanceMatrixCacheWithTTLs(st, time.Minute, time.Minute)
	first, err := cache.GetMatricesWithWindowWithPool("legacy-trader", "AI", "POOLUSDT", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
	if err != nil {
		t.Fatalf("get pooled matrices first: %v", err)
	}
	if first == nil || len(first.Global) == 0 {
		t.Fatalf("expected global pool matrices to be populated, got %+v", first)
	}

	foundGlobalPoolKey := false
	cache.mu.RLock()
	for key := range cache.global {
		if strings.HasPrefix(key, performanceMatrixGlobalPoolKey+"::") {
			foundGlobalPoolKey = true
			break
		}
	}
	cache.mu.RUnlock()
	if !foundGlobalPoolKey {
		t.Fatalf("expected cache key with %q prefix in global cache map", performanceMatrixGlobalPoolKey+"::")
	}

	second, err := cache.GetMatricesWithWindowWithPool("brand-new-trader", "AI", "POOLUSDT", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
	if err != nil {
		t.Fatalf("get pooled matrices second: %v", err)
	}
	if second == nil || len(second.Global) == 0 {
		t.Fatalf("expected pooled matrices to be reused for new trader, got %+v", second)
	}
}

func TestPerformanceMatrixCacheBackcastGlobalPoolUsesRawFactorsAcrossTraders(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_performance_backcast_global_pool_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	baseTime := time.Now().UTC().Add(-2 * time.Hour).UnixMilli()
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

	if err := st.Shadow().CreateBatch([]*store.ShadowSnapshot{
		{
			TraderID:     "deleted-trader",
			DecisionTime: baseTime + 1,
			Symbol:       "POOLBACKUSDT",
			Sector:       "AI",
			HeatScore:    25,
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
		t.Fatalf("create batch: %v", err)
	}

	cache := newPerformanceMatrixCacheWithTTLs(st, time.Minute, time.Minute)
	backcast, err := cache.GetBackcastMatricesWithWindowWithPool("fresh-trader", "AI", "POOLBACKUSDT", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
	if err != nil {
		t.Fatalf("get pooled backcast matrices: %v", err)
	}
	if backcast == nil || len(backcast.Global) == 0 {
		t.Fatalf("expected non-empty pooled backcast matrices, got %+v", backcast)
	}
}

func TestPerformanceMatrixCacheBackcastGlobalPoolKeyedByWeightSignature(t *testing.T) {
	dbPath := filepath.Join("/tmp", "nofx_performance_backcast_signature_pool_test.db")
	_ = os.Remove(dbPath)
	t.Cleanup(func() { _ = os.Remove(dbPath) })

	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	market.SetAdaptiveWeightStore(st)
	defer market.SetAdaptiveWeightStore(nil)
	market.InvalidateAdaptiveWeightScope("")

	seedAdaptiveWeightHistory(t, st, "sig-trader-a", false)
	seedAdaptiveWeightHistory(t, st, "sig-trader-b", true)
	seedGlobalBackcastRawRows(t, st)

	cache := newPerformanceMatrixCacheWithTTLs(st, time.Minute, time.Minute)
	a, err := cache.GetBackcastMatricesWithWindowWithPool("sig-trader-a", "", "", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
	if err != nil {
		t.Fatalf("get trader A pooled backcast matrices: %v", err)
	}
	b, err := cache.GetBackcastMatricesWithWindowWithPool("sig-trader-b", "", "", store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
	if err != nil {
		t.Fatalf("get trader B pooled backcast matrices: %v", err)
	}
	if a == nil || len(a.Global) == 0 {
		t.Fatalf("expected non-empty trader A global backcast matrices, got %+v", a)
	}
	if b == nil || len(b.Global) == 0 {
		t.Fatalf("expected non-empty trader B global backcast matrices, got %+v", b)
	}
	if reflect.DeepEqual(a.Global, b.Global) {
		t.Fatalf("expected different backcast matrices for distinct weight signatures, got A=%+v B=%+v", a.Global, b.Global)
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	signatureKeys := make(map[string]struct{})
	for key := range cache.backcastGlobal {
		if strings.HasPrefix(key, performanceMatrixGlobalPoolKey+"::backcast::sig_") {
			signatureKeys[key] = struct{}{}
		}
	}
	if len(signatureKeys) != 2 {
		t.Fatalf("expected 2 signature-keyed global backcast cache entries, got %d keys: %+v", len(signatureKeys), signatureKeys)
	}
}

func seedAdaptiveWeightHistory(t *testing.T, st *store.Store, traderID string, marketLeads bool) {
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
			Symbol:             traderID + "-ADAPT-" + string(rune('A'+i%26)),
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
		t.Fatalf("create adaptive weight history for %s: %v", traderID, err)
	}
}

func seedGlobalBackcastRawRows(t *testing.T, st *store.Store) {
	t.Helper()

	baseTime := time.Now().UTC().Add(-2 * time.Hour)
	rows := []*store.ShadowSnapshot{
		{
			TraderID:     "deleted-trader",
			DecisionTime: baseTime.Add(1 * time.Minute).UnixMilli(),
			Symbol:       "POOL-TREND",
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
			Symbol:       "POOL-MARKET",
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
		t.Fatalf("create pooled raw-factor rows: %v", err)
	}
}
