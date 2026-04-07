package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StrategyProfileStore stores strategy capability profiles.
type StrategyProfileStore struct {
	db *gorm.DB
}

// NewStrategyProfileStore creates a strategy profile store.
func NewStrategyProfileStore(db *gorm.DB) *StrategyProfileStore {
	return &StrategyProfileStore{db: db}
}

// StrategyProfile is the persisted strategy-layer capability authorization model.
// It declares what a trader is allowed to do; runtime clipping still decides what is open in a given cycle.
type StrategyProfile struct {
	ID                     int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TraderID               string    `gorm:"column:trader_id;not null;uniqueIndex:idx_strategy_profiles_trader_id" json:"trader_id"` // System-owned trader key for this capability profile.
	Exchange               string    `gorm:"column:exchange;not null;default:binance_usdm" json:"exchange"`                          // Strategy authorization scope, fixed to binance_usdm in phase 1.
	Mode                   string    `gorm:"column:mode;not null;default:one_way" json:"mode"`                                       // Position mode authorization, fixed to one_way in phase 1.
	AllowAddPosition       bool      `gorm:"column:allow_add_position;default:false" json:"allow_add_position"`                      // Strategy-layer authorization only; not a runtime promise.
	AllowPartialTakeProfit bool      `gorm:"column:allow_partial_take_profit;default:false" json:"allow_partial_take_profit"`        // Strategy-layer authorization only; not a runtime promise.
	AllowMoveStopLoss      bool      `gorm:"column:allow_move_stop_loss;default:false" json:"allow_move_stop_loss"`                  // Strategy-layer authorization only; not a runtime promise.
	AllowTrailingStop      bool      `gorm:"column:allow_trailing_stop;default:false" json:"allow_trailing_stop"`                    // Strategy-layer authorization only; not a runtime promise.
	ProtectionMode         string    `gorm:"column:protection_mode;not null;default:fixed" json:"protection_mode"`                   // Protection policy label, not the live order state.
	MaxScaleInCount        int       `gorm:"column:max_scale_in_count;default:0" json:"max_scale_in_count"`                          // Strategy authorization ceiling for scale-in attempts.
	MaxPositionRiskPct     float64   `gorm:"column:max_position_risk_pct;default:0" json:"max_position_risk_pct"`                    // Strategy risk budget, derived from configured risk controls.
	DecisionStyle          string    `gorm:"column:decision_style;default:ai_standard" json:"decision_style"`                        // Human-readable decision style label.
	ExecutionEnabled       bool      `gorm:"column:execution_enabled;default:true" json:"execution_enabled"`                         // System authorization flag; false means the profile never enters live execution.
	CreatedAt              time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt              time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// TableName returns the table name for StrategyProfile.
func (StrategyProfile) TableName() string {
	return "strategy_profiles"
}

// initTables initializes the strategy profile table and indexes.
func (s *StrategyProfileStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'strategy_profiles'`).Scan(&tableExists)
		if tableExists > 0 {
			s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_strategy_profiles_trader_id ON strategy_profiles(trader_id)`)
			return nil
		}
	}

	if err := s.db.AutoMigrate(&StrategyProfile{}); err != nil {
		return fmt.Errorf("failed to migrate strategy_profiles table: %w", err)
	}

	if err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_strategy_profiles_trader_id ON strategy_profiles(trader_id)`).Error; err != nil {
		return fmt.Errorf("failed to create strategy profile index: %w", err)
	}

	return nil
}

// GetByTraderID gets the strategy profile for a trader.
func (s *StrategyProfileStore) GetByTraderID(traderID string) (*StrategyProfile, error) {
	var profile StrategyProfile
	err := s.db.Where("trader_id = ?", traderID).First(&profile).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get strategy profile: %w", err)
	}
	return &profile, nil
}

// BuildAndStoreFromFullConfig builds a strategy profile from trader configuration and persists it.
// The returned profile is strategy-layer authorization data, not a runtime clipping result.
func (s *StrategyProfileStore) BuildAndStoreFromFullConfig(full *TraderFullConfig) (*StrategyProfile, error) {
	if full == nil || full.Trader == nil {
		return nil, fmt.Errorf("trader configuration is required")
	}

	profile, err := buildStrategyProfileFromFullConfig(full)
	if err != nil {
		return nil, err
	}

	if existing, err := s.GetByTraderID(profile.TraderID); err == nil && existing != nil {
		profile.CreatedAt = existing.CreatedAt
	}

	if err := s.Upsert(profile); err != nil {
		return nil, err
	}

	return profile, nil
}

// Upsert persists a strategy profile using trader_id as the unique key.
func (s *StrategyProfileStore) Upsert(profile *StrategyProfile) error {
	if profile == nil {
		return fmt.Errorf("strategy profile is nil")
	}

	updates := []string{
		"exchange",
		"mode",
		"allow_add_position",
		"allow_partial_take_profit",
		"allow_move_stop_loss",
		"allow_trailing_stop",
		"protection_mode",
		"max_scale_in_count",
		"max_position_risk_pct",
		"decision_style",
		"execution_enabled",
		"updated_at",
	}

	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "trader_id"}},
		DoUpdates: clause.AssignmentColumns(updates),
	}).Create(profile).Error
}

// buildStrategyProfileFromFullConfig derives the persisted capability profile from trader configuration.
func buildStrategyProfileFromFullConfig(full *TraderFullConfig) (*StrategyProfile, error) {
	config := GetDefaultStrategyConfig("en")
	if full.Strategy != nil {
		if parsed, err := full.Strategy.ParseConfig(); err == nil && parsed != nil {
			config = *parsed
		}
	}
	config.ClampLimits()

	strategyType := strings.TrimSpace(strings.ToLower(config.StrategyType))
	if strategyType == "" {
		strategyType = "ai_trading"
	}

	if config.RiskControl.MaxMarginUsage <= 0 {
		config.RiskControl.MaxMarginUsage = GetDefaultStrategyConfig("en").RiskControl.MaxMarginUsage
	}

	now := time.Now().UTC()
	profile := &StrategyProfile{
		TraderID:               full.Trader.ID,
		Exchange:               "binance_usdm",
		Mode:                   "one_way",
		AllowAddPosition:       true,
		AllowPartialTakeProfit: false,
		AllowMoveStopLoss:      false,
		AllowTrailingStop:      false,
		ProtectionMode:         "fixed",
		MaxScaleInCount:        1,
		MaxPositionRiskPct:     config.RiskControl.MaxMarginUsage * 100,
		DecisionStyle:          strategyType,
		ExecutionEnabled:       full.Exchange != nil && full.Exchange.Enabled && strings.EqualFold(full.Exchange.ExchangeType, "binance"),
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	return profile, nil
}
