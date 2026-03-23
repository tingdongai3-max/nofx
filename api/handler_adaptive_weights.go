package api

import (
	"net/http"
	"strings"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleAdaptiveWeights(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Query("trader_id")
	scope := strings.ToLower(strings.TrimSpace(c.Query("scope")))
	target := strings.TrimSpace(c.Query("target"))
	symbol := c.Query("symbol")
	sector := c.Query("sector")

	traders, err := s.store.Trader().List(userID)
	if err != nil {
		SafeInternalError(c, "Load traders", err)
		return
	}
	if len(traders) == 0 {
		SafeNotFound(c, "Trader")
		return
	}

	if traderID == "" {
		traderID = traders[0].ID
	}

	owned := false
	for _, trader := range traders {
		if trader.ID == traderID {
			owned = true
			break
		}
	}
	if !owned {
		SafeForbidden(c, "Trader access denied")
		return
	}

	if scope != "" {
		switch scope {
		case "global":
			symbol = ""
			sector = ""
		case "sector":
			if target == "" {
				latest, err := s.store.Shadow().GetLatestByTrader(traderID)
				if err != nil {
					SafeInternalError(c, "Load latest shadow snapshot", err)
					return
				}
				if latest != nil {
					target = latest.Sector
				}
			}
			if target == "" {
				SafeBadRequest(c, "target is required for sector scope")
				return
			}
			symbol = ""
			sector = target
		case "symbol":
			if target == "" {
				latest, err := s.store.Shadow().GetLatestByTrader(traderID)
				if err != nil {
					SafeInternalError(c, "Load latest shadow snapshot", err)
					return
				}
				if latest != nil {
					target = latest.Symbol
				}
			}
			if target == "" {
				SafeBadRequest(c, "target is required for symbol scope")
				return
			}
			symbol = target
			if sector == "" {
				latest, err := s.store.Shadow().GetLatestBySymbol(traderID, symbol, false)
				if err != nil {
					SafeInternalError(c, "Load latest symbol shadow snapshot", err)
					return
				}
				if latest != nil {
					sector = latest.Sector
				}
			}
		default:
			SafeBadRequest(c, "scope must be one of: global, sector, symbol")
			return
		}
	} else if symbol == "" {
		latest, err := s.store.Shadow().GetLatestByTrader(traderID)
		if err != nil {
			SafeInternalError(c, "Load latest shadow snapshot", err)
			return
		}
		if latest != nil {
			symbol = latest.Symbol
			sector = latest.Sector
		}
	} else if sector == "" {
		latest, err := s.store.Shadow().GetLatestBySymbol(traderID, symbol, false)
		if err != nil {
			SafeInternalError(c, "Load latest symbol shadow snapshot", err)
			return
		}
		if latest != nil {
			sector = latest.Sector
		}
	}

	c.JSON(http.StatusOK, market.GetAdaptiveWeightState(traderID, sector, symbol))
}
