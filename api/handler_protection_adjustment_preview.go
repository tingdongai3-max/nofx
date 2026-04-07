package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeProtectionAdjustmentPreview returns the current read-only dynamic protection preview for one trader/symbol.
// It never changes execution behavior; it only exposes the protection adjustment truth state for debugging.
func (s *Server) handleRuntimeProtectionAdjustmentPreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	if autoTrader, err := s.traderManager.GetTrader(traderID); err == nil && autoTrader != nil {
		preview, previewErr := autoTrader.PreviewProtectionAdjustmentState(symbol)
		if previewErr == nil {
			c.JSON(http.StatusOK, preview)
			return
		}
		logger.Infof("protection adjustment preview fallback for trader %s: %v", traderID, previewErr)
	}

	preview, err := trader.BuildRuntimeProtectionAdjustmentPreview(s.store, traderID, symbol, false, 0)
	if err != nil {
		SafeInternalError(c, "Preview runtime protection adjustment", err)
		return
	}

	_ = userID
	c.JSON(http.StatusOK, preview)
}
