package api

import (
	"net/http"

	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeRestorePreview returns the cold-start restore validation preview for one trader/symbol.
// It is validation-only and does not expose new trading behavior.
func (s *Server) handleRuntimeRestorePreview(c *gin.Context) {
	userID := c.GetString("user_id")
	symbol := c.Query("symbol")

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	preview, err := trader.NewTruthLayerRestoreManager(s.store).RestoreTraderTruth(
		userID,
		traderID,
		symbol,
		"readonly",
		false,
		false,
	)
	if err != nil {
		SafeInternalError(c, "Preview truth-layer restore", err)
		return
	}

	c.JSON(http.StatusOK, preview)
}
