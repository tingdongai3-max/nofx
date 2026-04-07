package api

import (
	"net/http"

	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleRuntimeReplayPreview returns the validation replay preview for one stored fixture.
// It is validation-only and does not expose new trading behavior.
func (s *Server) handleRuntimeReplayPreview(c *gin.Context) {
	fixtureID := c.Query("fixture_id")
	replayMode := c.DefaultQuery("mode", "sequential")

	preview, err := trader.NewBinanceUserStreamReplayManager(s.store).RunFixture(fixtureID, replayMode)
	if err != nil {
		SafeInternalError(c, "Preview runtime replay", err)
		return
	}

	c.JSON(http.StatusOK, preview)
}
