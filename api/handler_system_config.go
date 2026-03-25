package api

import (
	"net/http"

	"nofx/market"
	"nofx/store"

	"github.com/gin-gonic/gin"
)

type updateRealBacktestConfigRequest struct {
	Enabled           *bool    `json:"enabled"`
	MaxMarginPerTrade *float64 `json:"rb_max_margin_per_trade"`
	ReserveMargin     *float64 `json:"rb_reserve_margin"`
	MinEVThreshold    *float64 `json:"rb_min_ev_threshold"`
	MaxEVThreshold    *float64 `json:"rb_max_ev_threshold"`
}

type updateAdaptiveMemoryConfigRequest struct {
	GlobalSamples *int `json:"adaptive_global_samples"`
	SectorSamples *int `json:"adaptive_sector_samples"`
	SymbolSamples *int `json:"adaptive_symbol_samples"`
}

type updateResonanceGuardConfigRequest struct {
	AdaptiveEntryFloor  *float64 `json:"adaptive_entry_floor"`
	AdaptiveEntryLambda *float64 `json:"adaptive_entry_lambda"`
}

func (s *Server) handleUpdateRealBacktestConfig(c *gin.Context) {
	var req updateRealBacktestConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "invalid request body")
		return
	}

	current, err := s.store.GetRealBacktestConfig()
	if err != nil {
		SafeInternalError(c, "Load real backtest config", err)
		return
	}

	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}
	if req.MaxMarginPerTrade != nil {
		current.MaxMarginPerTrade = *req.MaxMarginPerTrade
	}
	if req.ReserveMargin != nil {
		current.ReserveMargin = *req.ReserveMargin
	}
	if req.MinEVThreshold != nil {
		current.MinEVThreshold = *req.MinEVThreshold
	}
	if req.MaxEVThreshold != nil {
		current.MaxEVThreshold = *req.MaxEVThreshold
	}

	if current.MaxMarginPerTrade <= 0 {
		SafeBadRequest(c, "rb_max_margin_per_trade must be greater than 0")
		return
	}
	if current.ReserveMargin < 0 {
		SafeBadRequest(c, "rb_reserve_margin must be greater than or equal to 0")
		return
	}
	if current.MinEVThreshold < 0 {
		SafeBadRequest(c, "rb_min_ev_threshold must be greater than or equal to 0")
		return
	}
	if current.MaxEVThreshold <= current.MinEVThreshold {
		SafeBadRequest(c, "rb_max_ev_threshold must be greater than rb_min_ev_threshold")
		return
	}

	if err := s.store.SetRealBacktestConfig(current); err != nil {
		SafeInternalError(c, "Update real backtest config", err)
		return
	}
	if err := s.traderManager.SyncGlobalResonanceSniper(); err != nil {
		SafeInternalError(c, "Sync global resonance sniper", err)
		return
	}

	c.JSON(http.StatusOK, current)
}

func (s *Server) handleUpdateAdaptiveMemoryConfig(c *gin.Context) {
	var req updateAdaptiveMemoryConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "invalid request body")
		return
	}

	current := store.DefaultAdaptiveMemoryConfig()
	loadedConfig, err := s.store.GetAdaptiveMemoryConfig()
	if err != nil {
		SafeInternalError(c, "Load adaptive memory config", err)
		return
	}
	current = loadedConfig

	if req.GlobalSamples != nil {
		current.GlobalSamples = *req.GlobalSamples
	}
	if req.SectorSamples != nil {
		current.SectorSamples = *req.SectorSamples
	}
	if req.SymbolSamples != nil {
		current.SymbolSamples = *req.SymbolSamples
	}

	if current.GlobalSamples < store.AdaptiveGlobalSamplesMin || current.GlobalSamples > store.AdaptiveGlobalSamplesMax {
		SafeBadRequest(c, "adaptive_global_samples must be between 500 and 10000")
		return
	}
	if current.SectorSamples < store.AdaptiveSectorSamplesMin || current.SectorSamples > store.AdaptiveSectorSamplesMax {
		SafeBadRequest(c, "adaptive_sector_samples must be between 300 and 5000")
		return
	}
	if current.SymbolSamples < store.AdaptiveSymbolSamplesMin || current.SymbolSamples > store.AdaptiveSymbolSamplesMax {
		SafeBadRequest(c, "adaptive_symbol_samples must be between 100 and 3000")
		return
	}

	if err := s.store.SetAdaptiveMemoryConfig(current); err != nil {
		SafeInternalError(c, "Update adaptive memory config", err)
		return
	}
	market.InvalidateAdaptiveWeightScope("")

	c.JSON(http.StatusOK, current)
}

func (s *Server) handleUpdateResonanceGuardConfig(c *gin.Context) {
	var req updateResonanceGuardConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "invalid request body")
		return
	}

	current := store.DefaultResonanceGuardConfig()
	loadedConfig, err := s.store.GetResonanceGuardConfig()
	if err != nil {
		SafeInternalError(c, "Load resonance guard config", err)
		return
	}
	current = loadedConfig

	if req.AdaptiveEntryFloor != nil {
		current.AdaptiveEntryFloor = *req.AdaptiveEntryFloor
	}
	if req.AdaptiveEntryLambda != nil {
		current.AdaptiveEntryLambda = *req.AdaptiveEntryLambda
	}

	if current.AdaptiveEntryFloor < 0 || current.AdaptiveEntryFloor > 60 {
		SafeBadRequest(c, "adaptive_entry_floor must be between 0 and 60")
		return
	}
	if current.AdaptiveEntryLambda < 0 || current.AdaptiveEntryLambda > 1 {
		SafeBadRequest(c, "adaptive_entry_lambda must be between 0 and 1")
		return
	}

	if err := s.store.SetResonanceGuardConfig(current); err != nil {
		SafeInternalError(c, "Update resonance guard config", err)
		return
	}

	c.JSON(http.StatusOK, current)
}
