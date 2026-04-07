package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeCapabilityPreview returns the current read-only capability preview for one trader/symbol.
// It never changes execution behavior; it only exposes the clipped capability state for debugging.
func (s *Server) handleRuntimeCapabilityPreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	if autoTrader, err := s.traderManager.GetTrader(traderID); err == nil && autoTrader != nil {
		preview, previewErr := autoTrader.PreviewRuntimeCapabilities(symbol)
		if previewErr == nil {
			c.JSON(http.StatusOK, preview)
			return
		}
		logger.Infof("runtime capability preview fallback for trader %s: %v", traderID, previewErr)
	}

	preview, err := trader.BuildRuntimeCapabilityPreview(
		s.store,
		userID,
		traderID,
		symbol,
		"readonly",
		nil,
		nil,
		nil,
		nil,
		nil,
		false,
		nil,
	)
	if err != nil {
		SafeInternalError(c, "Preview runtime capabilities", err)
		return
	}

	c.JSON(http.StatusOK, preview)
}
