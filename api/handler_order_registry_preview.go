package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeOrderRegistryPreview returns the Binance-only order truth preview for one trader/symbol.
// It is read-only and never changes execution behavior.
func (s *Server) handleRuntimeOrderRegistryPreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	if autoTrader, err := s.traderManager.GetTrader(traderID); err == nil && autoTrader != nil {
		preview, previewErr := autoTrader.PreviewOrderState(symbol)
		if previewErr == nil {
			c.JSON(http.StatusOK, preview)
			return
		}
		logger.Infof("order registry preview fallback for trader %s: %v", traderID, previewErr)
	}

	preview, err := trader.BuildRuntimeOrderPreview(s.store, traderID, symbol, false)
	if err != nil {
		SafeInternalError(c, "Preview runtime orders", err)
		return
	}

	// userID is intentionally read so auth remains part of the request contract even though the preview is trader-scoped.
	_ = userID
	c.JSON(http.StatusOK, preview)
}
