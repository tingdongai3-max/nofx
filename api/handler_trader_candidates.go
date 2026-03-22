package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleTraderCandidates returns the latest in-memory candidate market snapshot
// for the specified trader. The payload is aligned with the most recent AI cycle.
func (s *Server) handleTraderCandidates(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	autoTrader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	snapshot := autoTrader.GetCandidateSnapshot()
	c.JSON(http.StatusOK, snapshot)
}
