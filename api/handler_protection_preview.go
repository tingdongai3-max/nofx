package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeProtectionPreview returns the current read-only protection truth preview for one trader/symbol.
// It never changes execution behavior; it only exposes the protection truth state for debugging.
func (s *Server) handleRuntimeProtectionPreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	if autoTrader, err := s.traderManager.GetTrader(traderID); err == nil && autoTrader != nil {
		preview, previewErr := autoTrader.PreviewProtectionState(symbol)
		if previewErr == nil {
			c.JSON(http.StatusOK, preview)
			return
		}
		logger.Infof("protection preview fallback for trader %s: %v", traderID, previewErr)
	}

	preview, err := trader.BuildRuntimeProtectionPreview(s.store, traderID, symbol, false)
	if err != nil {
		SafeInternalError(c, "Preview runtime protection", err)
		return
	}

	_ = userID
	c.JSON(http.StatusOK, preview)
}
