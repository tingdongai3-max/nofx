package trader

import (
	"fmt"
	"math"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"strings"
	"sync"
	"time"
)

const (
	resonanceClusterCacheTTL        = 5 * time.Minute
	resonanceClusterRefreshStep     = 50
	resonanceClusterMinimumPositive = 20
)

type resonanceClusterCacheEntry struct {
	cluster             *kernel.ResonanceFeatureCluster
	positiveSampleCount int
	expiresAt           time.Time
}

type featureArchetypeCacheEntry struct {
	library             *kernel.FeatureArchetypeLibrary
	positiveSampleCount int
	expiresAt           time.Time
}

var resonanceClusterCache sync.Map
var featureArchetypeCache sync.Map

func init() {
	store.RegisterCacheRefreshHook(func() error {
		clearResonanceClusterCache()
		return nil
	})
}

func clearResonanceClusterCache() {
	resonanceClusterCache.Range(func(key, _ any) bool {
		resonanceClusterCache.Delete(key)
		return true
	})
	featureArchetypeCache.Range(func(key, _ any) bool {
		featureArchetypeCache.Delete(key)
		return true
	})
}

func (at *AutoTrader) loadResonanceFeatureCluster(symbol, sector string) (*kernel.ResonanceFeatureCluster, string, error) {
	if at == nil || at.store == nil {
		return nil, "", nil
	}

	normalizedSymbol := market.Normalize(symbol)
	normalizedSector := strings.TrimSpace(sector)
	scopes := []struct {
		name string
		key  string
		rows func() ([]*store.ShadowSnapshot, error)
	}{
		{
			name: "symbol",
			key:  resonanceClusterCacheKey(at.id, "symbol", normalizedSector, normalizedSymbol, false),
			rows: func() ([]*store.ShadowSnapshot, error) {
				if normalizedSymbol == "" {
					return nil, nil
				}
				return at.store.Shadow().ListPerformanceSnapshotsBySymbol(at.id, normalizedSymbol, false)
			},
		},
		{
			name: "sector",
			key:  resonanceClusterCacheKey(at.id, "sector", normalizedSector, "", false),
			rows: func() ([]*store.ShadowSnapshot, error) {
				if normalizedSector == "" {
					return nil, nil
				}
				return at.store.Shadow().ListPerformanceSnapshotsBySector(at.id, normalizedSector, false)
			},
		},
		{
			name: "global",
			key:  resonanceClusterCacheKey(at.id, "global", "", "", false),
			rows: func() ([]*store.ShadowSnapshot, error) {
				return at.store.Shadow().ListPerformanceSnapshotsByTrader(at.id, false)
			},
		},
	}

	for _, scope := range scopes {
		rows, err := scope.rows()
		if err != nil {
			return nil, "", err
		}
		cluster := loadResonanceClusterFromRows(scope.key, rows)
		if cluster != nil {
			return cluster, scope.name, nil
		}
	}

	return nil, "", nil
}

func loadResonanceClusterFromRows(cacheKey string, rows []*store.ShadowSnapshot) *kernel.ResonanceFeatureCluster {
	now := time.Now().UTC()
	built := kernel.BuildResonanceFeatureClusterFromSnapshots(rows)
	positiveCount := countPositiveResonanceSnapshots(rows)
	effectivePositiveCount := positiveCount
	if built != nil {
		effectivePositiveCount = built.PositiveSampleCount
	}

	if cached, ok := resonanceClusterCache.Load(cacheKey); ok {
		entry := cached.(resonanceClusterCacheEntry)
		if now.Before(entry.expiresAt) {
			if positiveCount < resonanceClusterMinimumPositive {
				return entry.cluster
			}
			if built != nil && math.Abs(float64(effectivePositiveCount-entry.positiveSampleCount)) < resonanceClusterRefreshStep {
				return entry.cluster
			}
			if built == nil {
				return entry.cluster
			}
		}
	}

	if built == nil {
		resonanceClusterCache.Delete(cacheKey)
		return nil
	}

	resonanceClusterCache.Store(cacheKey, resonanceClusterCacheEntry{
		cluster:             built,
		positiveSampleCount: built.PositiveSampleCount,
		expiresAt:           now.Add(resonanceClusterCacheTTL),
	})
	return built
}

func (at *AutoTrader) loadFeatureArchetypeLibrary(symbol, sector string) (*kernel.FeatureArchetypeLibrary, string, error) {
	if at == nil || at.store == nil {
		return nil, "", nil
	}

	normalizedSymbol := market.Normalize(symbol)
	normalizedSector := strings.TrimSpace(sector)
	scopes := []struct {
		name string
		key  string
		rows func() ([]*store.ShadowSnapshot, error)
	}{
		{
			name: "symbol",
			key:  resonanceClusterCacheKey(at.id, "archetype_symbol", normalizedSector, normalizedSymbol, false),
			rows: func() ([]*store.ShadowSnapshot, error) {
				if normalizedSymbol == "" {
					return nil, nil
				}
				return at.store.Shadow().ListPerformanceSnapshotsBySymbol(at.id, normalizedSymbol, false)
			},
		},
		{
			name: "sector",
			key:  resonanceClusterCacheKey(at.id, "archetype_sector", normalizedSector, "", false),
			rows: func() ([]*store.ShadowSnapshot, error) {
				if normalizedSector == "" {
					return nil, nil
				}
				return at.store.Shadow().ListPerformanceSnapshotsBySector(at.id, normalizedSector, false)
			},
		},
		{
			name: "global",
			key:  resonanceClusterCacheKey(at.id, "archetype_global", "", "", false),
			rows: func() ([]*store.ShadowSnapshot, error) {
				return at.store.Shadow().ListPerformanceSnapshotsByTrader(at.id, false)
			},
		},
	}

	for _, scope := range scopes {
		rows, err := scope.rows()
		if err != nil {
			return nil, "", err
		}
		library := loadFeatureArchetypeLibraryFromRows(scope.key, rows)
		if library != nil {
			return library, scope.name, nil
		}
	}

	return nil, "", nil
}

func loadFeatureArchetypeLibraryFromRows(cacheKey string, rows []*store.ShadowSnapshot) *kernel.FeatureArchetypeLibrary {
	now := time.Now().UTC()
	built := kernel.BuildFeatureArchetypeLibraryFromSnapshots(rows)
	positiveCount := countPositiveResonanceSnapshots(rows)
	effectivePositiveCount := positiveCount
	if built != nil {
		for _, archetype := range built.Archetypes {
			if archetype == nil {
				continue
			}
			effectivePositiveCount += archetype.PositiveSampleCount
		}
	}

	if cached, ok := featureArchetypeCache.Load(cacheKey); ok {
		entry := cached.(featureArchetypeCacheEntry)
		if now.Before(entry.expiresAt) {
			if positiveCount < resonanceClusterMinimumPositive {
				return entry.library
			}
			if built != nil && math.Abs(float64(effectivePositiveCount-entry.positiveSampleCount)) < resonanceClusterRefreshStep {
				return entry.library
			}
			if built == nil {
				return entry.library
			}
		}
	}

	if built == nil {
		featureArchetypeCache.Delete(cacheKey)
		return nil
	}

	featureArchetypeCache.Store(cacheKey, featureArchetypeCacheEntry{
		library:             built,
		positiveSampleCount: effectivePositiveCount,
		expiresAt:           now.Add(resonanceClusterCacheTTL),
	})
	return built
}

func countPositiveResonanceSnapshots(rows []*store.ShadowSnapshot) int {
	count := 0
	for _, row := range rows {
		if row == nil || !row.Filled || row.ReturnPct <= 0 || (row.Incubating && !row.IncubationPromoted) {
			continue
		}
		count++
	}
	return count
}

func resonanceClusterCacheKey(traderID, scope, sector, symbol string, includeAllTraders bool) string {
	return fmt.Sprintf("%s|%s|%s|%s|%t", traderID, scope, sector, symbol, includeAllTraders)
}
