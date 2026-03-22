package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleShadowSnapshots(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Query("trader_id")
	limit := 200

	if rawLimit := c.Query("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			SafeBadRequest(c, "Invalid limit")
			return
		}
		if parsed > 500 {
			parsed = 500
		}
		limit = parsed
	}

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

	rows, err := s.store.Shadow().ListByTrader(traderID, limit)
	if err != nil {
		SafeInternalError(c, "Load shadow snapshots", err)
		return
	}

	c.JSON(http.StatusOK, rows)
}
