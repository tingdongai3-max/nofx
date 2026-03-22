package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ShadowSnapshot stores the full T0 candidate cross-section plus the T+N realized outcome.
type ShadowSnapshot struct {
	ID                 uint    `gorm:"primaryKey" json:"id"`
	TraderID           string  `gorm:"column:trader_id;not null;index:idx_shadow_trader_time,priority:1;uniqueIndex:uidx_shadow_batch_symbol,priority:1" json:"trader_id"`
	DecisionTime       int64   `gorm:"column:decision_time;not null;index:idx_shadow_trader_time,priority:2;index:idx_shadow_fill_due,priority:2;uniqueIndex:uidx_shadow_batch_symbol,priority:2" json:"decision_time"`
	Symbol             string  `gorm:"column:symbol;not null;uniqueIndex:uidx_shadow_batch_symbol,priority:3" json:"symbol"`
	Sector             string  `gorm:"column:sector;default:'';index" json:"sector"`
	ActionTaken        int     `gorm:"column:action_taken;not null;default:0" json:"action_taken"`
	PriceT0            float64 `gorm:"column:price_t0;not null;default:0" json:"price_t0"`
	HeatScore          float64 `gorm:"column:heat_score;default:0" json:"heat_score"`
	TradingSub         float64 `gorm:"column:trading_sub;default:0" json:"trading_sub"`
	QuantSub           float64 `gorm:"column:quant_sub;default:0" json:"quant_sub"`
	MarketFactor       float64 `gorm:"column:market_factor;default:0" json:"market_factor"`
	TrendFactor        float64 `gorm:"column:trend_factor;default:0" json:"trend_factor"`
	DonchianFactor     float64 `gorm:"column:donchian_factor;default:0" json:"-"`
	VolumeSpikeFactor  float64 `gorm:"column:volume_spike_factor;default:0" json:"volume_spike_factor"`
	MTFResonanceFactor float64 `gorm:"column:mtf_resonance_factor;default:0" json:"-"`
	QuantFactor        float64 `gorm:"column:quant_factor;default:0" json:"quant_factor"`
	SocialFactor       float64 `gorm:"column:social_factor;default:0" json:"social_factor"`
	OnChainFactor      float64 `gorm:"column:onchain_factor;default:0" json:"onchain_factor"`
	VolUtilization     float64 `gorm:"column:vol_utilization;default:0" json:"vol_utilization"`
	FundingRate        float64 `gorm:"column:funding_rate;default:0" json:"funding_rate"`
	SourceSummary      string  `gorm:"column:source_summary;default:''" json:"source_summary"`
	Filled             bool    `gorm:"column:filled;not null;default:false;index:idx_shadow_fill_due,priority:1" json:"filled"`
	PriceT1            float64 `gorm:"column:price_t1;default:0" json:"price_t1"`
	ReturnPct          float64 `gorm:"column:return_pct;default:0" json:"return_pct"`
	FilledAt           int64   `gorm:"column:filled_at;default:0" json:"filled_at"`
	CreatedAt          int64   `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          int64   `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ShadowSnapshot) TableName() string {
	return "shadow_snapshots"
}

type ShadowSnapshotStore struct {
	db *gorm.DB
}

func NewShadowSnapshotStore(db *gorm.DB) *ShadowSnapshotStore {
	return &ShadowSnapshotStore{db: db}
}

func (s *ShadowSnapshotStore) initTables() error {
	return s.db.AutoMigrate(&ShadowSnapshot{})
}

func (s *ShadowSnapshotStore) CreateBatch(snapshots []*ShadowSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	nowMs := time.Now().UTC().UnixMilli()
	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		if snapshot.CreatedAt == 0 {
			snapshot.CreatedAt = nowMs
		}
		if snapshot.UpdatedAt == 0 {
			snapshot.UpdatedAt = snapshot.CreatedAt
		}
	}

	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "trader_id"},
			{Name: "decision_time"},
			{Name: "symbol"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"action_taken":         gorm.Expr("excluded.action_taken"),
			"price_t0":             gorm.Expr("excluded.price_t0"),
			"sector":               gorm.Expr("excluded.sector"),
			"heat_score":           gorm.Expr("excluded.heat_score"),
			"trading_sub":          gorm.Expr("excluded.trading_sub"),
			"quant_sub":            gorm.Expr("excluded.quant_sub"),
			"market_factor":        gorm.Expr("excluded.market_factor"),
			"trend_factor":         gorm.Expr("excluded.trend_factor"),
			"donchian_factor":      gorm.Expr("excluded.donchian_factor"),
			"volume_spike_factor":  gorm.Expr("excluded.volume_spike_factor"),
			"mtf_resonance_factor": gorm.Expr("excluded.mtf_resonance_factor"),
			"quant_factor":         gorm.Expr("excluded.quant_factor"),
			"social_factor":        gorm.Expr("excluded.social_factor"),
			"onchain_factor":       gorm.Expr("excluded.onchain_factor"),
			"vol_utilization":      gorm.Expr("excluded.vol_utilization"),
			"funding_rate":         gorm.Expr("excluded.funding_rate"),
			"source_summary":       gorm.Expr("excluded.source_summary"),
			"filled":               false,
			"price_t1":             0,
			"return_pct":           0,
			"filled_at":            0,
			"updated_at":           gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&snapshots).Error
}

func (s *ShadowSnapshotStore) MarkActionTaken(traderID string, decisionTime int64, symbols []string) error {
	if traderID == "" || decisionTime == 0 || len(symbols) == 0 {
		return nil
	}

	result := s.db.Model(&ShadowSnapshot{}).
		Where("trader_id = ? AND decision_time = ? AND symbol IN ?", traderID, decisionTime, symbols).
		Updates(map[string]interface{}{
			"action_taken": 1,
			"updated_at":   time.Now().UTC().UnixMilli(),
		})
	if result.Error != nil {
		return fmt.Errorf("failed to mark shadow actions: %w", result.Error)
	}
	return nil
}

func (s *ShadowSnapshotStore) GetPendingFill(cutoffDecisionTime int64, limit int) ([]*ShadowSnapshot, error) {
	if limit <= 0 {
		limit = 200
	}

	var snapshots []*ShadowSnapshot
	err := s.db.Where("filled = ? AND decision_time <= ?", false, cutoffDecisionTime).
		Order("decision_time ASC, id ASC").
		Limit(limit).
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query pending shadow snapshots: %w", err)
	}
	return snapshots, nil
}

func (s *ShadowSnapshotStore) MarkFilled(id uint, priceT1, returnPct float64, filledAt int64) error {
	result := s.db.Model(&ShadowSnapshot{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"filled":     true,
			"price_t1":   priceT1,
			"return_pct": returnPct,
			"filled_at":  filledAt,
			"updated_at": filledAt,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to mark shadow snapshot filled: %w", result.Error)
	}
	return nil
}

func (s *ShadowSnapshotStore) ListByTrader(traderID string, limit int) ([]*ShadowSnapshot, error) {
	if limit <= 0 {
		limit = 200
	}

	var snapshots []*ShadowSnapshot
	err := s.db.Where("trader_id = ?", traderID).
		Order("decision_time DESC, id DESC").
		Limit(limit).
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list shadow snapshots: %w", err)
	}
	return snapshots, nil
}

func (s *ShadowSnapshotStore) ListFilledForAdaptive(traderID string, limit int) ([]*ShadowSnapshot, error) {
	if limit <= 0 {
		limit = 500
	}

	query := s.db.Where("filled = ?", true)
	if traderID != "" {
		query = query.Where("trader_id = ?", traderID)
	}

	var snapshots []*ShadowSnapshot
	err := query.Order("decision_time DESC, id DESC").
		Limit(limit).
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list adaptive shadow samples: %w", err)
	}
	return snapshots, nil
}

func (s *ShadowSnapshotStore) ListFilledForSectorAdaptive(traderID, sector string, limit int) ([]*ShadowSnapshot, error) {
	if limit <= 0 {
		limit = 3000
	}
	if traderID == "" || sector == "" {
		return nil, nil
	}

	var snapshots []*ShadowSnapshot
	err := s.db.Where("filled = ? AND trader_id = ? AND sector = ?", true, traderID, sector).
		Order("decision_time DESC, id DESC").
		Limit(limit).
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list sector adaptive samples: %w", err)
	}
	return snapshots, nil
}

func (s *ShadowSnapshotStore) ListFilledForCoinAdaptive(traderID, symbol string, limit int) ([]*ShadowSnapshot, error) {
	if limit <= 0 {
		limit = 2000
	}
	if traderID == "" || symbol == "" {
		return nil, nil
	}

	var snapshots []*ShadowSnapshot
	err := s.db.Where("filled = ? AND trader_id = ? AND symbol = ?", true, traderID, symbol).
		Order("decision_time DESC, id DESC").
		Limit(limit).
		Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list coin adaptive samples: %w", err)
	}
	return snapshots, nil
}

func (s *ShadowSnapshotStore) GetLatestByTrader(traderID string) (*ShadowSnapshot, error) {
	if traderID == "" {
		return nil, nil
	}

	var snapshot ShadowSnapshot
	err := s.db.Where("trader_id = ?", traderID).
		Order("decision_time DESC, id DESC").
		First(&snapshot).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest shadow snapshot: %w", err)
	}
	return &snapshot, nil
}

func (s *ShadowSnapshotStore) GetLatestBySymbol(traderID, symbol string) (*ShadowSnapshot, error) {
	if traderID == "" || symbol == "" {
		return nil, nil
	}

	var snapshot ShadowSnapshot
	err := s.db.Where("trader_id = ? AND symbol = ?", traderID, symbol).
		Order("decision_time DESC, id DESC").
		First(&snapshot).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest shadow snapshot by symbol: %w", err)
	}
	return &snapshot, nil
}
