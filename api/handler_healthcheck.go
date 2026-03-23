package api

import (
	"net/http"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleFullStackHealthCheck(c *gin.Context) {
	if err := market.FullStackHealthCheck(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "degraded",
			"error":  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"full_stack": true,
	})
}
