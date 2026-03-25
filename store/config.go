package store

import (
	"strconv"
	"strings"
)

const (
	SystemConfigRealBacktestEnabled   = "real_backtest_enabled"
	SystemConfigRBMaxMarginPerTrade   = "rb_max_margin_per_trade"
	SystemConfigRBReserveMargin       = "rb_reserve_margin"
	SystemConfigRBMinEVThreshold      = "rb_min_ev_threshold"
	SystemConfigRBMaxEVThreshold      = "rb_max_ev_threshold"
	SystemConfigAdaptiveEntryFloor    = "adaptive_entry_floor"
	SystemConfigAdaptiveEntryLambda   = "adaptive_entry_lambda"
	SystemConfigAdaptiveGlobalSamples = "adaptive_global_samples"
	SystemConfigAdaptiveSectorSamples = "adaptive_sector_samples"
	SystemConfigAdaptiveSymbolSamples = "adaptive_symbol_samples"
)

const (
	defaultRBMaxMarginPerTrade   = 5.0
	defaultRBReserveMargin       = 6.0
	defaultRBMinEVThreshold      = 0.001
	defaultRBMaxEVThreshold      = 0.0025
	defaultAdaptiveEntryFloor    = 45.0
	defaultAdaptiveEntryLambda   = 0.5
	DefaultAdaptiveGlobalSamples = 5000
	DefaultAdaptiveSectorSamples = 3000
	DefaultAdaptiveSymbolSamples = 1000
	AdaptiveGlobalSamplesMin     = 500
	AdaptiveGlobalSamplesMax     = 10000
	AdaptiveSectorSamplesMin     = 300
	AdaptiveSectorSamplesMax     = 5000
	AdaptiveSymbolSamplesMin     = 100
	AdaptiveSymbolSamplesMax     = 3000
)

type RealBacktestSystemConfig struct {
	Enabled           bool    `json:"real_backtest_enabled"`
	MaxMarginPerTrade float64 `json:"rb_max_margin_per_trade"`
	ReserveMargin     float64 `json:"rb_reserve_margin"`
	MinEVThreshold    float64 `json:"rb_min_ev_threshold"`
	MaxEVThreshold    float64 `json:"rb_max_ev_threshold"`
}

type AdaptiveMemorySystemConfig struct {
	GlobalSamples int `json:"adaptive_global_samples"`
	SectorSamples int `json:"adaptive_sector_samples"`
	SymbolSamples int `json:"adaptive_symbol_samples"`
}

type ResonanceGuardSystemConfig struct {
	AdaptiveEntryFloor  float64 `json:"adaptive_entry_floor"`
	AdaptiveEntryLambda float64 `json:"adaptive_entry_lambda"`
}

func DefaultResonanceGuardConfig() ResonanceGuardSystemConfig {
	return ResonanceGuardSystemConfig{
		AdaptiveEntryFloor:  defaultAdaptiveEntryFloor,
		AdaptiveEntryLambda: defaultAdaptiveEntryLambda,
	}
}

func DefaultAdaptiveMemoryConfig() AdaptiveMemorySystemConfig {
	return AdaptiveMemorySystemConfig{
		GlobalSamples: DefaultAdaptiveGlobalSamples,
		SectorSamples: DefaultAdaptiveSectorSamples,
		SymbolSamples: DefaultAdaptiveSymbolSamples,
	}
}

func (s *Store) GetSystemConfigBool(key string, defaultValue bool) (bool, error) {
	if s == nil {
		return defaultValue, nil
	}

	raw, err := s.GetSystemConfig(key)
	if err != nil {
		if isMissingSystemConfigTableError(err) {
			return defaultValue, nil
		}
		return defaultValue, err
	}
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return defaultValue, nil
	}

	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return defaultValue, nil
	}
	return parsed, nil
}

func (s *Store) SetSystemConfigBool(key string, value bool) error {
	if s == nil {
		return nil
	}
	return s.SetSystemConfig(key, strconv.FormatBool(value))
}

func (s *Store) GetSystemConfigFloat(key string, defaultValue float64) (float64, error) {
	if s == nil {
		return defaultValue, nil
	}

	raw, err := s.GetSystemConfig(key)
	if err != nil {
		if isMissingSystemConfigTableError(err) {
			return defaultValue, nil
		}
		return defaultValue, err
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return defaultValue, nil
	}
	return parsed, nil
}

func (s *Store) SetSystemConfigFloat(key string, value float64) error {
	if s == nil {
		return nil
	}
	return s.SetSystemConfig(key, strconv.FormatFloat(value, 'f', -1, 64))
}

func (s *Store) GetSystemConfigInt(key string, defaultValue int) (int, error) {
	if s == nil {
		return defaultValue, nil
	}

	raw, err := s.GetSystemConfig(key)
	if err != nil {
		if isMissingSystemConfigTableError(err) {
			return defaultValue, nil
		}
		return defaultValue, err
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue, nil
	}
	return parsed, nil
}

func (s *Store) SetSystemConfigInt(key string, value int) error {
	if s == nil {
		return nil
	}
	return s.SetSystemConfig(key, strconv.Itoa(value))
}

func (s *Store) GetRealBacktestEnabled() (bool, error) {
	return s.GetSystemConfigBool(SystemConfigRealBacktestEnabled, false)
}

func (s *Store) SetRealBacktestEnabled(enabled bool) error {
	return s.SetSystemConfigBool(SystemConfigRealBacktestEnabled, enabled)
}

func (s *Store) GetRealBacktestConfig() (RealBacktestSystemConfig, error) {
	enabled, err := s.GetRealBacktestEnabled()
	if err != nil {
		return RealBacktestSystemConfig{}, err
	}

	maxMargin, err := s.GetSystemConfigFloat(SystemConfigRBMaxMarginPerTrade, defaultRBMaxMarginPerTrade)
	if err != nil {
		return RealBacktestSystemConfig{}, err
	}

	reserveMargin, err := s.GetSystemConfigFloat(SystemConfigRBReserveMargin, defaultRBReserveMargin)
	if err != nil {
		return RealBacktestSystemConfig{}, err
	}

	minEVThreshold, err := s.GetSystemConfigFloat(SystemConfigRBMinEVThreshold, defaultRBMinEVThreshold)
	if err != nil {
		return RealBacktestSystemConfig{}, err
	}

	maxEVThreshold, err := s.GetSystemConfigFloat(SystemConfigRBMaxEVThreshold, defaultRBMaxEVThreshold)
	if err != nil {
		return RealBacktestSystemConfig{}, err
	}

	return RealBacktestSystemConfig{
		Enabled:           enabled,
		MaxMarginPerTrade: maxMargin,
		ReserveMargin:     reserveMargin,
		MinEVThreshold:    minEVThreshold,
		MaxEVThreshold:    maxEVThreshold,
	}, nil
}

func (s *Store) SetRealBacktestConfig(config RealBacktestSystemConfig) error {
	if s == nil {
		return nil
	}
	if err := s.SetRealBacktestEnabled(config.Enabled); err != nil {
		return err
	}
	if err := s.SetSystemConfigFloat(SystemConfigRBMaxMarginPerTrade, config.MaxMarginPerTrade); err != nil {
		return err
	}
	if err := s.SetSystemConfigFloat(SystemConfigRBReserveMargin, config.ReserveMargin); err != nil {
		return err
	}
	if err := s.SetSystemConfigFloat(SystemConfigRBMinEVThreshold, config.MinEVThreshold); err != nil {
		return err
	}
	if err := s.SetSystemConfigFloat(SystemConfigRBMaxEVThreshold, config.MaxEVThreshold); err != nil {
		return err
	}
	return nil
}

func (s *Store) GetAdaptiveMemoryConfig() (AdaptiveMemorySystemConfig, error) {
	globalSamples, err := s.GetSystemConfigInt(SystemConfigAdaptiveGlobalSamples, DefaultAdaptiveGlobalSamples)
	if err != nil {
		return AdaptiveMemorySystemConfig{}, err
	}

	sectorSamples, err := s.GetSystemConfigInt(SystemConfigAdaptiveSectorSamples, DefaultAdaptiveSectorSamples)
	if err != nil {
		return AdaptiveMemorySystemConfig{}, err
	}

	symbolSamples, err := s.GetSystemConfigInt(SystemConfigAdaptiveSymbolSamples, DefaultAdaptiveSymbolSamples)
	if err != nil {
		return AdaptiveMemorySystemConfig{}, err
	}

	return AdaptiveMemorySystemConfig{
		GlobalSamples: globalSamples,
		SectorSamples: sectorSamples,
		SymbolSamples: symbolSamples,
	}, nil
}

func (s *Store) SetAdaptiveMemoryConfig(config AdaptiveMemorySystemConfig) error {
	if s == nil {
		return nil
	}
	if err := s.SetSystemConfigInt(SystemConfigAdaptiveGlobalSamples, config.GlobalSamples); err != nil {
		return err
	}
	if err := s.SetSystemConfigInt(SystemConfigAdaptiveSectorSamples, config.SectorSamples); err != nil {
		return err
	}
	if err := s.SetSystemConfigInt(SystemConfigAdaptiveSymbolSamples, config.SymbolSamples); err != nil {
		return err
	}
	return nil
}

func (s *Store) GetResonanceGuardConfig() (ResonanceGuardSystemConfig, error) {
	entryFloor, err := s.GetSystemConfigFloat(SystemConfigAdaptiveEntryFloor, defaultAdaptiveEntryFloor)
	if err != nil {
		return ResonanceGuardSystemConfig{}, err
	}

	entryLambda, err := s.GetSystemConfigFloat(SystemConfigAdaptiveEntryLambda, defaultAdaptiveEntryLambda)
	if err != nil {
		return ResonanceGuardSystemConfig{}, err
	}

	return ResonanceGuardSystemConfig{
		AdaptiveEntryFloor:  entryFloor,
		AdaptiveEntryLambda: entryLambda,
	}, nil
}

func (s *Store) SetResonanceGuardConfig(config ResonanceGuardSystemConfig) error {
	if s == nil {
		return nil
	}
	if err := s.SetSystemConfigFloat(SystemConfigAdaptiveEntryFloor, config.AdaptiveEntryFloor); err != nil {
		return err
	}
	if err := s.SetSystemConfigFloat(SystemConfigAdaptiveEntryLambda, config.AdaptiveEntryLambda); err != nil {
		return err
	}
	return nil
}

func isMissingSystemConfigTableError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table: system_config") ||
		strings.Contains(message, "relation \"system_config\" does not exist")
}
