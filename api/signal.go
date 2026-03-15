package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"nofx/ta"
)

// SignalPoolHolder 信号池holder接口
type SignalPoolHolder interface {
	GetSignalPool() *ta.SignalPool
}

// RegisterSignalRoutes 注册信号相关路由
func RegisterSignalRoutes(r *gin.Engine, poolHolder SignalPoolHolder) {
	signalGroup := r.Group("/api/signals")
	{
		// 获取统计信息
		signalGroup.GET("/stats", func(c *gin.Context) {
			pool := poolHolder.GetSignalPool()
			stats, err := pool.GetStats()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, stats)
		})

		// 获取待结算信号
		signalGroup.GET("/pending", func(c *gin.Context) {
			pool := poolHolder.GetSignalPool()
			signals, err := pool.GetPendingSignals()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"signals": signals})
		})

		// 获取所有信号（分页）
		signalGroup.GET("/list", func(c *gin.Context) {
			// TODO: 实现分页查询
			c.JSON(http.StatusOK, gin.H{"message": "not implemented"})
		})
	}
}
