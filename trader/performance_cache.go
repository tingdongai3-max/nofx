package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strings"
	"sync"
	"time"
)

const (
	performanceMatrixSymbolCacheTTL       = 15 * time.Minute
	performanceMatrixGlobalSectorCacheTTL = 30 * time.Minute
	performanceMatrixGlobalPoolKey        = "global_pool"
)

type performanceBinCacheEntry struct {
	bins      []*store.ScoreBinPerformance
	expiresAt time.Time
}

type PerformanceMatrixCache struct {
	store           *store.Store
	symbolTTL       time.Duration
	globalSectorTTL time.Duration

	mu     sync.RWMutex
	global map[string]performanceBinCacheEntry
	sector map[string]performanceBinCacheEntry
	symbol map[string]performanceBinCacheEntry

	backcastGlobal map[string]performanceBinCacheEntry
	backcastSector map[string]performanceBinCacheEntry
	backcastSymbol map[string]performanceBinCacheEntry
}

func NewPerformanceMatrixCache(st *store.Store, ttl time.Duration) *PerformanceMatrixCache {
	if ttl <= 0 {
		ttl = performanceMatrixSymbolCacheTTL
	}
	return newPerformanceMatrixCacheWithTTLs(st, ttl, performanceMatrixGlobalSectorCacheTTL)
}

func newPerformanceMatrixCacheWithTTLs(st *store.Store, symbolTTL, globalSectorTTL time.Duration) *PerformanceMatrixCache {
	cache := &PerformanceMatrixCache{
		store:           st,
		symbolTTL:       symbolTTL,
		globalSectorTTL: globalSectorTTL,
		global:          make(map[string]performanceBinCacheEntry),
		sector:          make(map[string]performanceBinCacheEntry),
		symbol:          make(map[string]performanceBinCacheEntry),
		backcastGlobal:  make(map[string]performanceBinCacheEntry),
		backcastSector:  make(map[string]performanceBinCacheEntry),
		backcastSymbol:  make(map[string]performanceBinCacheEntry),
	}

	store.RegisterCacheRefreshHook(cache.Clear)
	return cache
}

func (c *PerformanceMatrixCache) Provider() kernel.PerformanceBinProvider {
	return c.GetMatrices
}

func (c *PerformanceMatrixCache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.global = make(map[string]performanceBinCacheEntry)
	c.sector = make(map[string]performanceBinCacheEntry)
	c.symbol = make(map[string]performanceBinCacheEntry)
	c.backcastGlobal = make(map[string]performanceBinCacheEntry)
	c.backcastSector = make(map[string]performanceBinCacheEntry)
	c.backcastSymbol = make(map[string]performanceBinCacheEntry)
	return nil
}

func (c *PerformanceMatrixCache) GetMatrices(traderID, sector, symbol string) (*kernel.PerformanceBinMatrices, error) {
	return c.GetMatricesWithWindow(traderID, sector, symbol, store.PERFORMANCE_WINDOW_SIZE_DEFAULT)
}

func (c *PerformanceMatrixCache) GetMatricesWithWindow(traderID, sector, symbol string, windowSize int) (*kernel.PerformanceBinMatrices, error) {
	return c.getMatricesWithWindow(traderID, sector, symbol, windowSize, false)
}

func (c *PerformanceMatrixCache) GetMatricesWithWindowWithPool(traderID, sector, symbol string, windowSize int, includeAllTraders bool) (*kernel.PerformanceBinMatrices, error) {
	return c.getMatricesWithWindow(traderID, sector, symbol, windowSize, includeAllTraders)
}

func (c *PerformanceMatrixCache) getMatricesWithWindow(traderID, sector, symbol string, windowSize int, includeAllTraders bool) (*kernel.PerformanceBinMatrices, error) {
	if c == nil || c.store == nil || (traderID == "" && !includeAllTraders) {
		return nil, nil
	}

	windowSize = store.NormalizePerformanceWindowSize(windowSize)
	globalBins, err := c.getGlobal(traderID, windowSize, includeAllTraders)
	if err != nil {
		return nil, err
	}

	var sectorBins []*store.ScoreBinPerformance
	if sector != "" {
		sectorBins, err = c.getSector(traderID, sector, windowSize, includeAllTraders)
		if err != nil {
			return nil, err
		}
	}

	var symbolBins []*store.ScoreBinPerformance
	if symbol != "" {
		symbolBins, err = c.getSymbol(traderID, symbol, windowSize, includeAllTraders)
		if err != nil {
			return nil, err
		}
	}

	return &kernel.PerformanceBinMatrices{
		Global: globalBins,
		Sector: sectorBins,
		Symbol: symbolBins,
	}, nil
}

func (c *PerformanceMatrixCache) GetBackcastMatrices(traderID, sector, symbol string) (*kernel.PerformanceBinMatrices, error) {
	return c.GetBackcastMatricesWithWindowFiltered(traderID, sector, symbol, store.PERFORMANCE_WINDOW_SIZE_DEFAULT, true)
}

func (c *PerformanceMatrixCache) GetBackcastMatricesWithWindow(traderID, sector, symbol string, windowSize int) (*kernel.PerformanceBinMatrices, error) {
	return c.GetBackcastMatricesWithWindowFiltered(traderID, sector, symbol, windowSize, true)
}

func (c *PerformanceMatrixCache) GetBackcastMatricesWithWindowWithPool(traderID, sector, symbol string, windowSize int, includeAllTraders bool) (*kernel.PerformanceBinMatrices, error) {
	return c.GetBackcastMatricesWithWindowWithPoolFiltered(traderID, sector, symbol, windowSize, includeAllTraders, true)
}

func (c *PerformanceMatrixCache) GetBackcastMatricesWithWindowFiltered(
	traderID, sector, symbol string,
	windowSize int,
	resonanceFiltered bool,
) (*kernel.PerformanceBinMatrices, error) {
	return c.getBackcastMatricesWithWindow(traderID, sector, symbol, windowSize, false, resonanceFiltered)
}

func (c *PerformanceMatrixCache) GetBackcastMatricesWithWindowWithPoolFiltered(
	traderID, sector, symbol string,
	windowSize int,
	includeAllTraders bool,
	resonanceFiltered bool,
) (*kernel.PerformanceBinMatrices, error) {
	return c.getBackcastMatricesWithWindow(traderID, sector, symbol, windowSize, includeAllTraders, resonanceFiltered)
}

func (c *PerformanceMatrixCache) getBackcastMatricesWithWindow(traderID, sector, symbol string, windowSize int, includeAllTraders bool, resonanceFiltered bool) (*kernel.PerformanceBinMatrices, error) {
	if c == nil || c.store == nil || (traderID == "" && !includeAllTraders) {
		return nil, nil
	}

	windowSize = store.NormalizePerformanceWindowSize(windowSize)
	globalBins, err := c.getBackcastGlobal(traderID, windowSize, includeAllTraders, resonanceFiltered)
	if err != nil {
		return nil, err
	}

	var sectorBins []*store.ScoreBinPerformance
	if sector != "" {
		sectorBins, err = c.getBackcastSector(traderID, sector, windowSize, includeAllTraders, resonanceFiltered)
		if err != nil {
			return nil, err
		}
	}

	var symbolBins []*store.ScoreBinPerformance
	if symbol != "" {
		symbolBins, err = c.getBackcastSymbol(traderID, sector, symbol, windowSize, includeAllTraders, resonanceFiltered)
		if err != nil {
			return nil, err
		}
	}

	return &kernel.PerformanceBinMatrices{
		Global: globalBins,
		Sector: sectorBins,
		Symbol: symbolBins,
	}, nil
}

func (c *PerformanceMatrixCache) getGlobal(traderID string, windowSize int, includeAllTraders bool) ([]*store.ScoreBinPerformance, error) {
	cacheKey := performanceGlobalCacheKey(traderID, windowSize, includeAllTraders)
	return c.getOrLoad("snapshot_global", c.global, cacheKey, c.globalSectorTTL, func() ([]*store.ScoreBinPerformance, error) {
		return c.store.Shadow().ListSmoothedPerformanceBinsByTrader(traderID, windowSize, includeAllTraders)
	})
}

func (c *PerformanceMatrixCache) getSector(traderID, sector string, windowSize int, includeAllTraders bool) ([]*store.ScoreBinPerformance, error) {
	cacheKey := performanceSectorCacheKey(traderID, sector, windowSize, includeAllTraders)
	return c.getOrLoad("snapshot_sector", c.sector, cacheKey, c.globalSectorTTL, func() ([]*store.ScoreBinPerformance, error) {
		return c.store.Shadow().ListRawPerformanceBinsBySector(traderID, sector, includeAllTraders)
	})
}

func (c *PerformanceMatrixCache) getSymbol(traderID, symbol string, windowSize int, includeAllTraders bool) ([]*store.ScoreBinPerformance, error) {
	cacheKey := performanceSymbolCacheKey(traderID, symbol, windowSize, includeAllTraders)
	return c.getOrLoad("snapshot_symbol", c.symbol, cacheKey, c.symbolTTL, func() ([]*store.ScoreBinPerformance, error) {
		return c.store.Shadow().ListRawPerformanceBinsBySymbol(traderID, symbol, includeAllTraders)
	})
}

func (c *PerformanceMatrixCache) getBackcastGlobal(traderID string, windowSize int, includeAllTraders bool, resonanceFiltered bool) ([]*store.ScoreBinPerformance, error) {
	state := market.GetAdaptiveWeightState(traderID, "", "")
	cacheKey := performanceBackcastGlobalCacheKey(traderID, windowSize, includeAllTraders, resonanceFiltered, state.GetWeightSignature())
	return c.getOrLoad("backcast_global", c.backcastGlobal, cacheKey, c.globalSectorTTL, func() ([]*store.ScoreBinPerformance, error) {
		queryTraderID := traderID
		if includeAllTraders {
			queryTraderID = ""
		}
		rows, err := c.store.Shadow().ListFilledForAdaptive(queryTraderID, store.PERFORMANCE_RECENT_SAMPLE_LIMIT)
		if err != nil {
			return nil, err
		}
		return c.recalculateBackcastBins(rows, state, windowSize, resonanceFiltered)
	})
}

func (c *PerformanceMatrixCache) getBackcastSector(traderID, sector string, windowSize int, includeAllTraders bool, resonanceFiltered bool) ([]*store.ScoreBinPerformance, error) {
	state := market.GetAdaptiveWeightState(traderID, sector, "")
	cacheKey := performanceBackcastSectorCacheKey(traderID, sector, windowSize, includeAllTraders, resonanceFiltered, state.GetWeightSignature())
	return c.getOrLoad("backcast_sector", c.backcastSector, cacheKey, c.globalSectorTTL, func() ([]*store.ScoreBinPerformance, error) {
		queryTraderID := traderID
		if includeAllTraders {
			queryTraderID = ""
		}
		rows, err := c.store.Shadow().ListFilledForSectorAdaptive(queryTraderID, sector, store.PERFORMANCE_RECENT_SAMPLE_LIMIT)
		if err != nil {
			return nil, err
		}
		return c.recalculateBackcastBins(rows, state, windowSize, resonanceFiltered)
	})
}

func (c *PerformanceMatrixCache) getBackcastSymbol(traderID, sector, symbol string, windowSize int, includeAllTraders bool, resonanceFiltered bool) ([]*store.ScoreBinPerformance, error) {
	resolvedSector := sector
	if resolvedSector == "" {
		latest, err := c.store.Shadow().GetLatestBySymbol(traderID, symbol, includeAllTraders)
		if err != nil {
			return nil, err
		}
		if latest != nil {
			resolvedSector = latest.Sector
		}
	}

	state := market.GetAdaptiveWeightState(traderID, resolvedSector, symbol)
	cacheKey := performanceBackcastSymbolCacheKey(traderID, resolvedSector, symbol, windowSize, includeAllTraders, resonanceFiltered, state.GetWeightSignature())
	return c.getOrLoad("backcast_symbol", c.backcastSymbol, cacheKey, c.symbolTTL, func() ([]*store.ScoreBinPerformance, error) {
		queryTraderID := traderID
		if includeAllTraders {
			queryTraderID = ""
		}
		rows, err := c.store.Shadow().ListFilledForCoinAdaptive(queryTraderID, symbol, store.PERFORMANCE_RECENT_SAMPLE_LIMIT)
		if err != nil {
			return nil, err
		}
		return c.recalculateBackcastBins(rows, state, windowSize, resonanceFiltered)
	})
}

func (c *PerformanceMatrixCache) recalculateBackcastBins(
	rows []*store.ShadowSnapshot,
	state market.AdaptiveWeightState,
	windowSize int,
	resonanceFiltered bool,
) ([]*store.ScoreBinPerformance, error) {
	filteredRows := filterRowsWithRawFactors(rows)
	if len(filteredRows) == 0 {
		return nil, nil
	}

	if !resonanceFiltered || c == nil || c.store == nil {
		return market.RecalculateHistoricalFactors(filteredRows, state, windowSize), nil
	}

	guardConfig, err := c.store.GetResonanceGuardConfig()
	if err != nil {
		return nil, err
	}
	filter := kernel.BuildResonanceSnapshotFilter(filteredRows, guardConfig.AdaptiveEntryFloor, guardConfig.AdaptiveEntryLambda, 2.0)
	if filter == nil {
		return market.RecalculateHistoricalFactors(filteredRows, state, windowSize), nil
	}
	return market.RecalculateHistoricalFactors(filteredRows, state, windowSize, filter), nil
}

func (c *PerformanceMatrixCache) getOrLoad(
	cacheName string,
	cache map[string]performanceBinCacheEntry,
	key string,
	ttl time.Duration,
	load func() ([]*store.ScoreBinPerformance, error),
) ([]*store.ScoreBinPerformance, error) {
	now := time.Now().UTC()

	c.mu.RLock()
	entry, ok := cache[key]
	c.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		logger.Infof("PERFORMANCE_CACHE_HIT: cache=%s key=%s", cacheName, key)
		return cloneScoreBins(entry.bins), nil
	}

	logger.Infof("PERFORMANCE_CACHE_MISS: cache=%s key=%s", cacheName, key)
	bins, err := load()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	cache[key] = performanceBinCacheEntry{
		bins:      cloneScoreBins(bins),
		expiresAt: now.Add(ttl),
	}
	c.mu.Unlock()

	return cloneScoreBins(bins), nil
}

func cloneScoreBins(src []*store.ScoreBinPerformance) []*store.ScoreBinPerformance {
	if len(src) == 0 {
		return nil
	}

	cloned := make([]*store.ScoreBinPerformance, 0, len(src))
	for _, row := range src {
		if row == nil {
			continue
		}
		copyRow := *row
		cloned = append(cloned, &copyRow)
	}
	return cloned
}

func performancePoolTag(traderID string, includeAllTraders bool) string {
	if includeAllTraders {
		return performanceMatrixGlobalPoolKey
	}
	return traderID
}

func performanceGlobalCacheKey(traderID string, windowSize int, includeAllTraders bool) string {
	return fmt.Sprintf("%s::w%d", performancePoolTag(traderID, includeAllTraders), store.NormalizePerformanceWindowSize(windowSize))
}

func performanceSectorCacheKey(traderID, sector string, windowSize int, includeAllTraders bool) string {
	return fmt.Sprintf("%s::%s::w%d", performancePoolTag(traderID, includeAllTraders), sector, store.NormalizePerformanceWindowSize(windowSize))
}

func performanceSymbolCacheKey(traderID, symbol string, windowSize int, includeAllTraders bool) string {
	return fmt.Sprintf("%s::%s::w%d", performancePoolTag(traderID, includeAllTraders), symbol, store.NormalizePerformanceWindowSize(windowSize))
}

func performanceBackcastPoolTag(traderID string, includeAllTraders bool, resonanceFiltered bool, signature string) string {
	return fmt.Sprintf("%s::backcast::rf_%t::sig_%s", performancePoolTag(traderID, includeAllTraders), resonanceFiltered, signature)
}

func performanceBackcastGlobalCacheKey(traderID string, windowSize int, includeAllTraders bool, resonanceFiltered bool, signature string) string {
	return fmt.Sprintf("%s::w%d", performanceBackcastPoolTag(traderID, includeAllTraders, resonanceFiltered, signature), store.NormalizePerformanceWindowSize(windowSize))
}

func performanceBackcastSectorCacheKey(traderID, sector string, windowSize int, includeAllTraders bool, resonanceFiltered bool, signature string) string {
	return fmt.Sprintf("%s::%s::w%d", performanceBackcastPoolTag(traderID, includeAllTraders, resonanceFiltered, signature), sector, store.NormalizePerformanceWindowSize(windowSize))
}

func performanceBackcastSymbolCacheKey(traderID, sector, symbol string, windowSize int, includeAllTraders bool, resonanceFiltered bool, signature string) string {
	return fmt.Sprintf("%s::%s::%s::w%d", performanceBackcastPoolTag(traderID, includeAllTraders, resonanceFiltered, signature), sector, symbol, store.NormalizePerformanceWindowSize(windowSize))
}

func filterRowsWithRawFactors(rows []*store.ShadowSnapshot) []*store.ShadowSnapshot {
	if len(rows) == 0 {
		return nil
	}
	filtered := make([]*store.ShadowSnapshot, 0, len(rows))
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.RawFactors) == "" {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}
