package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeScaleOutPreview returns the current read-only scale-out truth preview for one trader/symbol.
// It never changes execution behavior; it only exposes the scale-out truth state for debugging.
func (s *Server) handleRuntimeScaleOutPreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	if autoTrader, err := s.traderManager.GetTrader(traderID); err == nil && autoTrader != nil {
		preview, previewErr := autoTrader.PreviewScaleOutState(symbol)
		if previewErr == nil {
			c.JSON(http.StatusOK, preview)
			return
		}
		logger.Infof("scale-out preview fallback for trader %s: %v", traderID, previewErr)
	}

	preview, err := trader.BuildRuntimeScaleOutPreview(s.store, traderID, symbol, false)
	if err != nil {
		SafeInternalError(c, "Preview runtime scale-out", err)
		return
	}

	_ = userID
	c.JSON(http.StatusOK, preview)
}

