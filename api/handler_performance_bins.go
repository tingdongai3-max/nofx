package api

import (
	"net/http"
	"nofx/kernel"
	"nofx/market"
	"nofx/store"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *Server) handlePerformanceBins(c *gin.Context) {
	s.handlePerformanceBinsRequest(c, false)
}

func (s *Server) handlePerformanceBinsBackcast(c *gin.Context) {
	s.handlePerformanceBinsRequest(c, true)
}

func (s *Server) handlePerformanceBinsRequest(c *gin.Context, backcast bool) {
	traderID, ok := s.resolveOwnedTraderID(c)
	if !ok {
		return
	}

	scope, sector, symbol, ok := s.resolvePerformanceScope(c, traderID)
	if !ok {
		return
	}

	windowSize, ok := s.resolvePerformanceWindowSize(c)
	if !ok {
		return
	}

	rows, err := s.loadPerformanceBins(traderID, scope, sector, symbol, windowSize, backcast)
	if err != nil {
		label := "Load performance bins"
		if backcast {
			label = "Load backcast performance bins"
		}
		SafeInternalError(c, label, err)
		return
	}

	c.JSON(http.StatusOK, rows)
}

func (s *Server) resolvePerformanceWindowSize(c *gin.Context) (int, bool) {
	rawWindow := strings.TrimSpace(c.Query("window_size"))
	if rawWindow == "" {
		return store.NormalizePerformanceWindowSize(0), true
	}

	windowSize, err := strconv.Atoi(rawWindow)
	if err != nil || windowSize <= 0 {
		SafeBadRequest(c, "window_size must be a positive integer")
		return 0, false
	}
	return store.NormalizePerformanceWindowSize(windowSize), true
}

func (s *Server) resolveOwnedTraderID(c *gin.Context) (string, bool) {
	userID := c.GetString("user_id")
	traderID := strings.TrimSpace(c.Query("trader_id"))

	traders, err := s.store.Trader().List(userID)
	if err != nil {
		SafeInternalError(c, "Load traders", err)
		return "", false
	}
	if len(traders) == 0 {
		SafeNotFound(c, "Trader")
		return "", false
	}

	if traderID == "" {
		traderID = traders[0].ID
	}

	for _, trader := range traders {
		if trader.ID == traderID {
			return traderID, true
		}
	}

	SafeForbidden(c, "Trader access denied")
	return "", false
}

func (s *Server) resolvePerformanceScope(
	c *gin.Context,
	traderID string,
) (scope string, sector string, symbol string, ok bool) {
	target := strings.TrimSpace(c.Query("target"))
	scope = strings.ToLower(strings.TrimSpace(c.DefaultQuery("scope", "global")))

	switch scope {
	case "", "global":
		return "global", "", "", true
	case "sector":
		if target == "" {
			latest, err := s.store.Shadow().GetLatestByTrader(traderID)
			if err != nil {
				SafeInternalError(c, "Load latest shadow snapshot", err)
				return "", "", "", false
			}
			if latest != nil {
				target = strings.TrimSpace(latest.Sector)
			}
		}
		if target == "" {
			SafeBadRequest(c, "target is required for sector scope")
			return "", "", "", false
		}
		return "sector", target, "", true
	case "symbol":
		if target == "" {
			latest, err := s.store.Shadow().GetLatestByTrader(traderID)
			if err != nil {
				SafeInternalError(c, "Load latest shadow snapshot", err)
				return "", "", "", false
			}
			if latest != nil {
				target = strings.TrimSpace(latest.Symbol)
			}
		}
		if target == "" {
			SafeBadRequest(c, "target is required for symbol scope")
			return "", "", "", false
		}

		latest, err := s.store.Shadow().GetLatestBySymbol(traderID, target, false)
		if err != nil {
			SafeInternalError(c, "Load latest symbol shadow snapshot", err)
			return "", "", "", false
		}
		if latest != nil {
			sector = strings.TrimSpace(latest.Sector)
		}
		return "symbol", sector, target, true
	default:
		SafeBadRequest(c, "scope must be one of: global, sector, symbol")
		return "", "", "", false
	}
}

func (s *Server) loadPerformanceBins(
	traderID, scope, sector, symbol string,
	windowSize int,
	backcast bool,
) ([]*store.ScoreBinPerformance, error) {
	loadWithPool := func(includeAllTraders bool) ([]*store.ScoreBinPerformance, error) {
		if s.performanceCache != nil {
			var (
				matrices *kernel.PerformanceBinMatrices
				err      error
			)
			if backcast {
				matrices, err = s.performanceCache.GetBackcastMatricesWithWindowWithPool(traderID, sector, symbol, windowSize, includeAllTraders)
			} else {
				matrices, err = s.performanceCache.GetMatricesWithWindowWithPool(traderID, sector, symbol, windowSize, includeAllTraders)
			}
			if err != nil {
				return nil, err
			}
			if matrices != nil {
				switch scope {
				case "global":
					return matrices.Global, nil
				case "sector":
					return matrices.Sector, nil
				case "symbol":
					return matrices.Symbol, nil
				}
			}
		}

		if backcast {
			switch scope {
			case "global":
				rows, err := s.store.Shadow().ListPerformanceSnapshotsByTrader(traderID, includeAllTraders)
				if err != nil {
					return nil, err
				}
				return market.RecalculateHistoricalBinsWithWindow(filterSnapshotsWithRawFactors(rows), market.GetAdaptiveWeightState(traderID, "", ""), windowSize), nil
			case "sector":
				rows, err := s.store.Shadow().ListPerformanceSnapshotsBySector(traderID, sector, includeAllTraders)
				if err != nil {
					return nil, err
				}
				sectorBins := market.RecalculateHistoricalBinsWithWindow(filterSnapshotsWithRawFactors(rows), market.GetAdaptiveWeightState(traderID, sector, ""), windowSize)
				globalRows, err := s.store.Shadow().ListPerformanceSnapshotsByTrader(traderID, includeAllTraders)
				if err != nil {
					return nil, err
				}
				globalBins := market.RecalculateHistoricalBinsWithWindow(filterSnapshotsWithRawFactors(globalRows), market.GetAdaptiveWeightState(traderID, "", ""), windowSize)
				return store.SmoothPerformanceBins(sectorBins, globalBins, "global"), nil
			case "symbol":
				rows, err := s.store.Shadow().ListPerformanceSnapshotsBySymbol(traderID, symbol, includeAllTraders)
				if err != nil {
					return nil, err
				}
				symbolBins := market.RecalculateHistoricalBinsWithWindow(filterSnapshotsWithRawFactors(rows), market.GetAdaptiveWeightState(traderID, sector, symbol), windowSize)
				globalRows, err := s.store.Shadow().ListPerformanceSnapshotsByTrader(traderID, includeAllTraders)
				if err != nil {
					return nil, err
				}
				globalBins := market.RecalculateHistoricalBinsWithWindow(filterSnapshotsWithRawFactors(globalRows), market.GetAdaptiveWeightState(traderID, "", ""), windowSize)
				if strings.TrimSpace(sector) == "" {
					return store.SmoothPerformanceBins(symbolBins, globalBins, "global"), nil
				}
				sectorRows, err := s.store.Shadow().ListPerformanceSnapshotsBySector(traderID, sector, includeAllTraders)
				if err != nil {
					return nil, err
				}
				sectorBins := market.RecalculateHistoricalBinsWithWindow(filterSnapshotsWithRawFactors(sectorRows), market.GetAdaptiveWeightState(traderID, sector, ""), windowSize)
				return store.SmoothPerformanceBinsWithFallbackChain(symbolBins, sectorBins, "sector", globalBins, "global"), nil
			}
		}

		switch scope {
		case "global":
			return s.store.Shadow().ListSmoothedPerformanceBinsByTrader(traderID, windowSize, includeAllTraders)
		case "sector":
			return s.store.Shadow().ListSmoothedPerformanceBinsBySector(traderID, sector, windowSize, includeAllTraders)
		case "symbol":
			return s.store.Shadow().ListSmoothedPerformanceBinsBySymbol(traderID, symbol, windowSize, includeAllTraders)
		default:
			return nil, nil
		}
	}

	rows, err := loadWithPool(false)
	if err != nil {
		return nil, err
	}
	if !hasPerformanceBins(rows) {
		return loadWithPool(true)
	}
	return rows, nil
}

func hasPerformanceBins(rows []*store.ScoreBinPerformance) bool {
	for _, row := range rows {
		if row != nil && row.TradeCount > 0 {
			return true
		}
	}
	return false
}

func filterSnapshotsWithRawFactors(rows []*store.ShadowSnapshot) []*store.ShadowSnapshot {
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
