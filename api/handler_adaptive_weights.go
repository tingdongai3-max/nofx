package api

import (
	"net/http"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleAdaptiveWeights(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Query("trader_id")
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

	if symbol == "" {
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
		latest, err := s.store.Shadow().GetLatestBySymbol(traderID, symbol)
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
