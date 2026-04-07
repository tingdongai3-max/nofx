package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeScaleInPreview returns the current read-only scale-in truth preview for one trader/symbol.
// It never changes execution behavior; it only exposes the add-position truth state for debugging.
func (s *Server) handleRuntimeScaleInPreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	if autoTrader, err := s.traderManager.GetTrader(traderID); err == nil && autoTrader != nil {
		preview, previewErr := autoTrader.PreviewScaleInState(symbol)
		if previewErr == nil {
			c.JSON(http.StatusOK, preview)
			return
		}
		logger.Infof("scale-in preview fallback for trader %s: %v", traderID, previewErr)
	}

	preview, err := trader.BuildRuntimeScaleInPreview(s.store, traderID, symbol, false, 0)
	if err != nil {
		SafeInternalError(c, "Preview runtime scale-in", err)
		return
	}

	_ = userID
	c.JSON(http.StatusOK, preview)
}
