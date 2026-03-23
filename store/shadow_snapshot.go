package store

import (
	"encoding/json"
	"fmt"
	"math"
	"nofx/logger"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	cacheRefreshHooksMu sync.RWMutex
	cacheRefreshHooks   []func() error
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
	QuantOIRaw         float64 `gorm:"column:quant_oi_raw;default:0" json:"-"`
	QuantImbalanceRaw  float64 `gorm:"column:quant_imbalance_raw;default:0" json:"-"`
	QuantNetflowRaw    float64 `gorm:"column:quant_netflow_raw;default:0" json:"-"`
	SocialFactor       float64 `gorm:"column:social_factor;default:0" json:"social_factor"`
	SocialRankRaw      float64 `gorm:"column:social_rank_raw;default:0" json:"-"`
	SocialUpvoteRaw    float64 `gorm:"column:social_upvote_raw;default:0" json:"-"`
	OnChainFactor      float64 `gorm:"column:onchain_factor;default:0" json:"onchain_factor"`
	OnChainRatioRaw    float64 `gorm:"column:onchain_ratio_raw;default:0" json:"-"`
	OnChainBuyRaw      float64 `gorm:"column:onchain_buy_raw;default:0" json:"-"`
	RawFactors         string  `gorm:"column:raw_factors;type:text;default:''" json:"raw_factors,omitempty"`
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

// ShadowRawFactors stores the underlying subfactor z-scores and availability
// captured at decision time so historical rows can be rescored later.
type ShadowRawFactors struct {
	Scores    map[string]float64 `json:"scores,omitempty"`
	Available map[string]bool    `json:"available,omitempty"`
}

func (f ShadowRawFactors) MarshalText() string {
	payload, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(payload)
}

func ParseShadowRawFactors(raw string) (ShadowRawFactors, error) {
	if strings.TrimSpace(raw) == "" {
		return ShadowRawFactors{}, nil
	}

	var factors ShadowRawFactors
	if err := json.Unmarshal([]byte(raw), &factors); err != nil {
		return ShadowRawFactors{}, fmt.Errorf("failed to parse shadow raw factors: %w", err)
	}
	if factors.Scores == nil {
		factors.Scores = make(map[string]float64)
	}
	if factors.Available == nil {
		factors.Available = make(map[string]bool)
	}
	return factors, nil
}

func (s *ShadowSnapshot) DecodedRawFactors() (ShadowRawFactors, error) {
	if s == nil {
		return ShadowRawFactors{}, nil
	}
	return ParseShadowRawFactors(s.RawFactors)
}

// ScoreBinPerformance summarizes realized performance for either a fixed score
// bucket or a sliding-window performance point.
type ScoreBinPerformance struct {
	BinStart                 int     `json:"bin_start"`
	BinLabel                 string  `json:"bin_label"`
	TradeCount               int     `json:"trade_count"`
	ExpectedValueLong        float64 `json:"ev_long"`
	MedianExpectedValueLong  float64 `json:"median_ev_long"`
	ProfitFactorLong         float64 `json:"profit_factor_long"`
	ExpectedValueShort       float64 `json:"ev_short"`
	MedianExpectedValueShort float64 `json:"median_ev_short"`
	ProfitFactorShort        float64 `json:"profit_factor_short"`
	Smoothed                 bool    `json:"smoothed,omitempty"`
	SmoothedBy               string  `json:"smoothed_by,omitempty"`
}

const (
	PERFORMANCE_SYMBOL_LIMIT        = 5000
	PERFORMANCE_TIME_WINDOW         = 7 * 24 * time.Hour
	CONFIDENCE_THRESHOLD_MIN        = 30
	PERFORMANCE_SMOOTHING_STEP      = 1
	PERFORMANCE_WINDOW_SIZE_DEFAULT = 5
	PERFORMANCE_WINDOW_SIZE_WIDE    = 10
	PerformanceBiasLong             = "LONG"
	PerformanceBiasShort            = "SHORT"
	PerformanceBiasWait             = "WAIT"
	performanceBinProfitFactorCap   = 99.9
	performanceBinSparseVisualLimit = 10
)

func NormalizePerformanceWindowSize(windowSize int) int {
	switch {
	case windowSize >= PERFORMANCE_WINDOW_SIZE_WIDE:
		return PERFORMANCE_WINDOW_SIZE_WIDE
	case windowSize > 0:
		return PERFORMANCE_WINDOW_SIZE_DEFAULT
	default:
		return PERFORMANCE_WINDOW_SIZE_DEFAULT
	}
}

func (ShadowSnapshot) TableName() string {
	return "shadow_snapshots"
}

// RegisterCacheRefreshHook registers a callback executed by RefreshCache.
func RegisterCacheRefreshHook(hook func() error) func() {
	if hook == nil {
		return func() {}
	}

	cacheRefreshHooksMu.Lock()
	index := len(cacheRefreshHooks)
	cacheRefreshHooks = append(cacheRefreshHooks, hook)
	cacheRefreshHooksMu.Unlock()

	return func() {
		cacheRefreshHooksMu.Lock()
		defer cacheRefreshHooksMu.Unlock()
		if index >= 0 && index < len(cacheRefreshHooks) {
			cacheRefreshHooks[index] = nil
		}
	}
}

// RefreshCache refreshes store-scoped cache hooks so runtime state is not left stale after deployment.
func RefreshCache() error {
	cacheRefreshHooksMu.RLock()
	hooks := make([]func() error, len(cacheRefreshHooks))
	copy(hooks, cacheRefreshHooks)
	cacheRefreshHooksMu.RUnlock()

	for index, hook := range hooks {
		if hook == nil {
			continue
		}
		executedAt := time.Now().UTC()
		if err := hook(); err != nil {
			logger.Warnf("RefreshCache hook executed at %v status=error index=%d err=%v", executedAt, index, err)
			return fmt.Errorf("refresh cache hook failed: %w", err)
		}
		logger.Infof("RefreshCache hook executed at %v status=ok index=%d", executedAt, index)
	}

	logger.Info("Store cache refresh completed")
	return nil
}

// NotifyMigration logs completion of the ShadowSnapshot schema migration flow.
func NotifyMigration() {
	logger.Info("ShadowSnapshot DB schema migration completed")
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
			"quant_oi_raw":         gorm.Expr("excluded.quant_oi_raw"),
			"quant_imbalance_raw":  gorm.Expr("excluded.quant_imbalance_raw"),
			"quant_netflow_raw":    gorm.Expr("excluded.quant_netflow_raw"),
			"social_factor":        gorm.Expr("excluded.social_factor"),
			"social_rank_raw":      gorm.Expr("excluded.social_rank_raw"),
			"social_upvote_raw":    gorm.Expr("excluded.social_upvote_raw"),
			"onchain_factor":       gorm.Expr("excluded.onchain_factor"),
			"onchain_ratio_raw":    gorm.Expr("excluded.onchain_ratio_raw"),
			"onchain_buy_raw":      gorm.Expr("excluded.onchain_buy_raw"),
			"raw_factors":          gorm.Expr("excluded.raw_factors"),
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

func (s *ShadowSnapshotStore) ListPerformanceBinsByTrader(traderID string, includeAllTraders bool) ([]*ScoreBinPerformance, error) {
	if !includeAllTraders && traderID == "" {
		return nil, nil
	}

	where := "trader_id = ?"
	args := []interface{}{traderID}
	if includeAllTraders {
		where = "1 = 1"
		args = nil
	}

	return s.listPerformanceBinsRaw(performanceBinQueryConfig{
		scopeLabel: "global",
		where:      where,
		args:       args,
		useTimeCut: true,
	})
}

func (s *ShadowSnapshotStore) ListSmoothedPerformanceBinsByTrader(traderID string, windowSize int, includeAllTraders bool) ([]*ScoreBinPerformance, error) {
	if !includeAllTraders && traderID == "" {
		return nil, nil
	}

	where := "trader_id = ?"
	args := []interface{}{traderID}
	if includeAllTraders {
		where = "1 = 1"
		args = nil
	}

	return s.listSmoothedPerformanceBins(performanceBinQueryConfig{
		scopeLabel: "global",
		where:      where,
		args:       args,
		useTimeCut: true,
	}, windowSize, func(row *ShadowSnapshot) (float64, bool) {
		if row == nil || !isFiniteScore(row.HeatScore) {
			return 0, false
		}
		return row.HeatScore, true
	})
}

func (s *ShadowSnapshotStore) ListPerformanceBinsBySector(traderID, sector string, includeAllTraders bool) ([]*ScoreBinPerformance, error) {
	if sector == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	globalWhere := "trader_id = ?"
	globalArgs := []interface{}{traderID}
	sectorWhere := "trader_id = ? AND sector = ?"
	sectorArgs := []interface{}{traderID, sector}
	if includeAllTraders {
		globalWhere = "1 = 1"
		globalArgs = nil
		sectorWhere = "sector = ?"
		sectorArgs = []interface{}{sector}
	}

	globalBins, err := s.listPerformanceBinsRaw(performanceBinQueryConfig{
		scopeLabel: "global",
		where:      globalWhere,
		args:       globalArgs,
		useTimeCut: true,
	})
	if err != nil {
		return nil, err
	}
	sectorBins, err := s.listPerformanceBinsRaw(performanceBinQueryConfig{
		scopeLabel: "sector",
		where:      sectorWhere,
		args:       sectorArgs,
		useTimeCut: true,
	})
	if err != nil {
		return nil, err
	}
	return SmoothPerformanceBins(sectorBins, globalBins, "global"), nil
}

func (s *ShadowSnapshotStore) ListSmoothedPerformanceBinsBySector(traderID, sector string, windowSize int, includeAllTraders bool) ([]*ScoreBinPerformance, error) {
	if sector == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	globalBins, err := s.ListSmoothedPerformanceBinsByTrader(traderID, windowSize, includeAllTraders)
	if err != nil {
		return nil, err
	}

	where := "trader_id = ? AND sector = ?"
	args := []interface{}{traderID, sector}
	if includeAllTraders {
		where = "sector = ?"
		args = []interface{}{sector}
	}

	sectorBins, err := s.listSmoothedPerformanceBins(performanceBinQueryConfig{
		scopeLabel: "sector",
		where:      where,
		args:       args,
		useTimeCut: true,
	}, windowSize, func(row *ShadowSnapshot) (float64, bool) {
		if row == nil || !isFiniteScore(row.HeatScore) {
			return 0, false
		}
		return row.HeatScore, true
	})
	if err != nil {
		return nil, err
	}
	return SmoothPerformanceBins(sectorBins, globalBins, "global"), nil
}

func (s *ShadowSnapshotStore) ListPerformanceBinsBySymbol(traderID, symbol string, includeAllTraders bool) ([]*ScoreBinPerformance, error) {
	if symbol == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	where := "trader_id = ? AND symbol = ?"
	args := []interface{}{traderID, symbol}
	if includeAllTraders {
		where = "symbol = ?"
		args = []interface{}{symbol}
	}

	symbolBins, err := s.listPerformanceBinsRaw(performanceBinQueryConfig{
		scopeLabel: "symbol",
		where:      where,
		args:       args,
		limit:      PERFORMANCE_SYMBOL_LIMIT,
	})
	if err != nil {
		return nil, err
	}

	latest, err := s.GetLatestBySymbol(traderID, symbol, includeAllTraders)
	if err != nil {
		return nil, err
	}

	globalBins, err := s.ListPerformanceBinsByTrader(traderID, includeAllTraders)
	if err != nil {
		return nil, err
	}

	if latest != nil && strings.TrimSpace(latest.Sector) != "" {
		sectorBins, err := s.ListPerformanceBinsBySector(traderID, latest.Sector, includeAllTraders)
		if err != nil {
			return nil, err
		}
		return SmoothPerformanceBinsWithFallbackChain(symbolBins, sectorBins, "sector", globalBins, "global"), nil
	}

	return SmoothPerformanceBins(symbolBins, globalBins, "global"), nil
}

func (s *ShadowSnapshotStore) ListSmoothedPerformanceBinsBySymbol(traderID, symbol string, windowSize int, includeAllTraders bool) ([]*ScoreBinPerformance, error) {
	if symbol == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	where := "trader_id = ? AND symbol = ?"
	args := []interface{}{traderID, symbol}
	if includeAllTraders {
		where = "symbol = ?"
		args = []interface{}{symbol}
	}

	symbolBins, err := s.listSmoothedPerformanceBins(performanceBinQueryConfig{
		scopeLabel: "symbol",
		where:      where,
		args:       args,
		limit:      PERFORMANCE_SYMBOL_LIMIT,
	}, windowSize, func(row *ShadowSnapshot) (float64, bool) {
		if row == nil || !isFiniteScore(row.HeatScore) {
			return 0, false
		}
		return row.HeatScore, true
	})
	if err != nil {
		return nil, err
	}

	latest, err := s.GetLatestBySymbol(traderID, symbol, includeAllTraders)
	if err != nil {
		return nil, err
	}

	globalBins, err := s.ListSmoothedPerformanceBinsByTrader(traderID, windowSize, includeAllTraders)
	if err != nil {
		return nil, err
	}

	if latest != nil && strings.TrimSpace(latest.Sector) != "" {
		sectorBins, err := s.ListSmoothedPerformanceBinsBySector(traderID, latest.Sector, windowSize, includeAllTraders)
		if err != nil {
			return nil, err
		}
		return SmoothPerformanceBinsWithFallbackChain(symbolBins, sectorBins, "sector", globalBins, "global"), nil
	}

	return SmoothPerformanceBins(symbolBins, globalBins, "global"), nil
}

type performanceBinQueryConfig struct {
	scopeLabel string
	where      string
	args       []interface{}
	useTimeCut bool
	limit      int
}

func (s *ShadowSnapshotStore) listPerformanceBinsRaw(cfg performanceBinQueryConfig) ([]*ScoreBinPerformance, error) {
	if strings.TrimSpace(cfg.where) == "" {
		return nil, nil
	}

	rows, err := s.listPerformanceSnapshotsRaw(cfg)
	if err != nil {
		return nil, err
	}

	return AggregatePerformanceBins(rows, func(row *ShadowSnapshot) (float64, bool) {
		if row == nil || !isFiniteScore(row.HeatScore) {
			return 0, false
		}
		return row.HeatScore, true
	}), nil
}

func (s *ShadowSnapshotStore) listSmoothedPerformanceBins(
	cfg performanceBinQueryConfig,
	windowSize int,
	scoreSelector func(*ShadowSnapshot) (float64, bool),
) ([]*ScoreBinPerformance, error) {
	if strings.TrimSpace(cfg.where) == "" {
		return nil, nil
	}

	rows, err := s.listPerformanceSnapshotsRaw(cfg)
	if err != nil {
		return nil, err
	}

	windowSize = NormalizePerformanceWindowSize(windowSize)
	logger.Infof("V3_AUDIT_PERF_SMOOTH: Scope=%s Step=%d Window=%d Samples=%d",
		cfg.scopeLabel,
		PERFORMANCE_SMOOTHING_STEP,
		windowSize,
		len(rows),
	)
	return AggregateSmoothedPerformanceBins(rows, scoreSelector, windowSize), nil
}

func (s *ShadowSnapshotStore) ListPerformanceSnapshotsByTrader(traderID string, includeAllTraders bool) ([]*ShadowSnapshot, error) {
	if !includeAllTraders && traderID == "" {
		return nil, nil
	}

	where := "trader_id = ?"
	args := []interface{}{traderID}
	if includeAllTraders {
		where = "1 = 1"
		args = nil
	}

	return s.listPerformanceSnapshotsRaw(performanceBinQueryConfig{
		scopeLabel: "global",
		where:      where,
		args:       args,
		useTimeCut: true,
	})
}

func (s *ShadowSnapshotStore) ListPerformanceSnapshotsBySector(traderID, sector string, includeAllTraders bool) ([]*ShadowSnapshot, error) {
	if sector == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	where := "trader_id = ? AND sector = ?"
	args := []interface{}{traderID, sector}
	if includeAllTraders {
		where = "sector = ?"
		args = []interface{}{sector}
	}

	return s.listPerformanceSnapshotsRaw(performanceBinQueryConfig{
		scopeLabel: "sector",
		where:      where,
		args:       args,
		useTimeCut: true,
	})
}

func (s *ShadowSnapshotStore) ListPerformanceSnapshotsBySymbol(traderID, symbol string, includeAllTraders bool) ([]*ShadowSnapshot, error) {
	if symbol == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	where := "trader_id = ? AND symbol = ?"
	args := []interface{}{traderID, symbol}
	if includeAllTraders {
		where = "symbol = ?"
		args = []interface{}{symbol}
	}

	return s.listPerformanceSnapshotsRaw(performanceBinQueryConfig{
		scopeLabel: "symbol",
		where:      where,
		args:       args,
		limit:      PERFORMANCE_SYMBOL_LIMIT,
	})
}

func (s *ShadowSnapshotStore) listPerformanceSnapshotsRaw(cfg performanceBinQueryConfig) ([]*ShadowSnapshot, error) {
	if strings.TrimSpace(cfg.where) == "" {
		return nil, nil
	}

	query := s.db.Where(cfg.where, cfg.args...).Where("filled = ? AND price_t1 > 0", true)
	if cfg.useTimeCut {
		cutoffMs := time.Now().UTC().Add(-PERFORMANCE_TIME_WINDOW).UnixMilli()
		logger.Infof("V3_AUDIT_PERF_SQL: Scope=%s Mode=time-window WindowHours=%.0f Limit=none CutoffMs=%d Where=%s Aggregation=go",
			cfg.scopeLabel,
			PERFORMANCE_TIME_WINDOW.Hours(),
			cutoffMs,
			cfg.where,
		)
		query = query.Where("decision_time > ?", cutoffMs)
	} else {
		limit := cfg.limit
		if limit <= 0 {
			limit = PERFORMANCE_SYMBOL_LIMIT
		}
		logger.Infof("V3_AUDIT_PERF_SQL: Scope=%s Mode=count-window WindowHours=none Limit=%d Where=%s Aggregation=go",
			cfg.scopeLabel,
			limit,
			cfg.where,
		)
		query = query.Limit(limit)
	}

	var snapshots []*ShadowSnapshot
	err := query.Order("decision_time DESC, id DESC").Find(&snapshots).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query performance snapshots: %w", err)
	}
	return snapshots, nil
}

func SmoothPerformanceBins(
	rawBins []*ScoreBinPerformance,
	fallbackBins []*ScoreBinPerformance,
	smoothedBy string,
) []*ScoreBinPerformance {
	return SmoothPerformanceBinsWithFallbackChain(rawBins, fallbackBins, smoothedBy, nil, "")
}

func SmoothPerformanceBinsWithFallbackChain(
	rawBins []*ScoreBinPerformance,
	primaryFallbackBins []*ScoreBinPerformance,
	primaryName string,
	secondaryFallbackBins []*ScoreBinPerformance,
	secondaryName string,
) []*ScoreBinPerformance {
	if len(rawBins) == 0 {
		return nil
	}

	primaryByBin := make(map[int]*ScoreBinPerformance, len(primaryFallbackBins))
	for _, bin := range primaryFallbackBins {
		if bin == nil {
			continue
		}
		primaryByBin[bin.BinStart] = bin
	}
	secondaryByBin := make(map[int]*ScoreBinPerformance, len(secondaryFallbackBins))
	for _, bin := range secondaryFallbackBins {
		if bin == nil {
			continue
		}
		secondaryByBin[bin.BinStart] = bin
	}

	smoothed := make([]*ScoreBinPerformance, 0, len(rawBins))
	for _, bin := range rawBins {
		if bin == nil {
			continue
		}

		next := *bin
		if next.TradeCount < CONFIDENCE_THRESHOLD_MIN {
			fallback := primaryByBin[next.BinStart]
			smoothedBy := primaryName
			if fallback == nil {
				fallback = secondaryByBin[next.BinStart]
				smoothedBy = secondaryName
			}
			if fallback != nil {
				rawWeight := float64(next.TradeCount) / float64(CONFIDENCE_THRESHOLD_MIN)
				fallbackWeight := 1 - rawWeight
				next.ExpectedValueLong = sanitizeExpectedValue(rawWeight*next.ExpectedValueLong + fallbackWeight*fallback.ExpectedValueLong)
				next.MedianExpectedValueLong = sanitizeExpectedValue(rawWeight*next.MedianExpectedValueLong + fallbackWeight*fallback.MedianExpectedValueLong)
				next.ProfitFactorLong = clampProfitFactor(rawWeight*next.ProfitFactorLong + fallbackWeight*fallback.ProfitFactorLong)
				next.ExpectedValueShort = sanitizeExpectedValue(rawWeight*next.ExpectedValueShort + fallbackWeight*fallback.ExpectedValueShort)
				next.MedianExpectedValueShort = sanitizeExpectedValue(rawWeight*next.MedianExpectedValueShort + fallbackWeight*fallback.MedianExpectedValueShort)
				next.ProfitFactorShort = clampProfitFactor(rawWeight*next.ProfitFactorShort + fallbackWeight*fallback.ProfitFactorShort)
				next.Smoothed = true
				next.SmoothedBy = smoothedBy
			}
		}
		smoothed = append(smoothed, &next)
	}

	return smoothed
}

func AggregatePerformanceBins(
	rows []*ShadowSnapshot,
	scoreSelector func(*ShadowSnapshot) (float64, bool),
) []*ScoreBinPerformance {
	samples := buildPerformanceSamples(rows, scoreSelector)
	if len(samples) == 0 {
		return nil
	}

	buckets := make(map[int]*performanceBucket)
	for _, sample := range samples {
		binStart := int(math.Floor(sample.score/5.0)) * 5
		if binStart < 0 {
			binStart = 0
		}
		if binStart > 100 {
			binStart = 100
		}

		bucket := buckets[binStart]
		if bucket == nil {
			bucket = &performanceBucket{}
			buckets[binStart] = bucket
		}
		applyPerformanceSample(bucket, sample)
	}

	return buildFixedPerformanceBins(buckets)
}

func AggregateSmoothedPerformanceBins(
	rows []*ShadowSnapshot,
	scoreSelector func(*ShadowSnapshot) (float64, bool),
	windowSize int,
) []*ScoreBinPerformance {
	samples := buildPerformanceSamples(rows, scoreSelector)
	if len(samples) == 0 {
		return nil
	}

	windowSize = NormalizePerformanceWindowSize(windowSize)
	halfWindow := float64(windowSize) / 2.0
	minScore := 100.0
	maxScore := 0.0
	for _, sample := range samples {
		if sample.score < minScore {
			minScore = sample.score
		}
		if sample.score > maxScore {
			maxScore = sample.score
		}
	}

	startCenter := int(math.Floor(minScore))
	endCenter := int(math.Ceil(maxScore))
	if startCenter < 0 {
		startCenter = 0
	}
	if endCenter > 100 {
		endCenter = 100
	}

	bins := make([]*ScoreBinPerformance, 0, endCenter-startCenter+1)
	for center := startCenter; center <= endCenter; center += PERFORMANCE_SMOOTHING_STEP {
		lower := float64(center) - halfWindow
		upper := float64(center) + halfWindow
		bucket := &performanceBucket{}
		for _, sample := range samples {
			if sample.score < lower || sample.score > upper {
				continue
			}
			applyPerformanceSample(bucket, sample)
		}
		if bucket.tradeCount == 0 {
			continue
		}

		profitFactorLong, profitFactorShort := calculateProfitFactors(bucket.sumPositive, bucket.sumNegative)
		bins = append(bins, &ScoreBinPerformance{
			BinStart:                 center,
			BinLabel:                 fmt.Sprintf("%d", center),
			TradeCount:               bucket.tradeCount,
			ExpectedValueLong:        sanitizeExpectedValue(bucket.sumLogLong / float64(bucket.tradeCount)),
			MedianExpectedValueLong:  sanitizeExpectedValue(medianFloat64(bucket.logLongs)),
			ProfitFactorLong:         profitFactorLong,
			ExpectedValueShort:       sanitizeExpectedValue(bucket.sumLogShort / float64(bucket.tradeCount)),
			MedianExpectedValueShort: sanitizeExpectedValue(medianFloat64(bucket.logShorts)),
			ProfitFactorShort:        profitFactorShort,
		})
	}

	return bins
}

func FindPerformanceBinForScore(
	bins []*ScoreBinPerformance,
	score float64,
) *ScoreBinPerformance {
	if len(bins) == 0 || !isFiniteScore(score) {
		return nil
	}

	clampedScore := clampScoreRange(score)
	var (
		bestBin      *ScoreBinPerformance
		bestDistance = math.MaxFloat64
	)
	for _, bin := range bins {
		if bin == nil || bin.TradeCount <= 0 {
			continue
		}
		distance := math.Abs(float64(bin.BinStart) - clampedScore)
		if distance < bestDistance {
			bestDistance = distance
			bestBin = bin
			continue
		}
		if distance == bestDistance && bestBin != nil && bin.TradeCount > bestBin.TradeCount {
			bestBin = bin
		}
	}
	return bestBin
}

func ResolvePerformanceBias(bin *ScoreBinPerformance) (string, float64) {
	if bin == nil {
		return PerformanceBiasWait, 0
	}

	longEligible := bin.ExpectedValueLong > 0 && bin.ProfitFactorLong > 1.2
	shortEligible := bin.ExpectedValueShort > 0 && bin.ProfitFactorShort > 1.2

	switch {
	case longEligible && (!shortEligible ||
		bin.ProfitFactorLong > bin.ProfitFactorShort ||
		(bin.ProfitFactorLong == bin.ProfitFactorShort && bin.ExpectedValueLong >= bin.ExpectedValueShort)):
		return PerformanceBiasLong, bin.ExpectedValueLong
	case shortEligible && (!longEligible ||
		bin.ProfitFactorShort > bin.ProfitFactorLong ||
		(bin.ProfitFactorShort == bin.ProfitFactorLong && bin.ExpectedValueShort > bin.ExpectedValueLong)):
		return PerformanceBiasShort, bin.ExpectedValueShort
	case bin.ProfitFactorLong > bin.ProfitFactorShort ||
		(bin.ProfitFactorLong == bin.ProfitFactorShort && bin.ExpectedValueLong >= bin.ExpectedValueShort):
		return PerformanceBiasWait, bin.ExpectedValueLong
	default:
		return PerformanceBiasWait, bin.ExpectedValueShort
	}
}

type performanceSample struct {
	score       float64
	returnPct   float64
	logLong     float64
	logShort    float64
	positivePnL float64
	negativePnL float64
}

type performanceBucket struct {
	tradeCount  int
	sumLogLong  float64
	sumLogShort float64
	logLongs    []float64
	logShorts   []float64
	sumPositive float64
	sumNegative float64
}

func buildPerformanceSamples(
	rows []*ShadowSnapshot,
	scoreSelector func(*ShadowSnapshot) (float64, bool),
) []performanceSample {
	if len(rows) == 0 {
		return nil
	}

	samples := make([]performanceSample, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Filled || !isFiniteScore(row.ReturnPct) {
			continue
		}

		score, ok := scoreSelector(row)
		if !ok || !isFiniteScore(score) {
			continue
		}

		sample := performanceSample{
			score:     clampScoreRange(score),
			returnPct: row.ReturnPct,
			logLong:   math.Log(clampLogReturnBase(1 + row.ReturnPct)),
			logShort:  math.Log(clampLogReturnBase(1 - row.ReturnPct)),
		}
		if row.ReturnPct > 0 {
			sample.positivePnL = row.ReturnPct
		} else if row.ReturnPct < 0 {
			sample.negativePnL = math.Abs(row.ReturnPct)
		}
		samples = append(samples, sample)
	}
	return samples
}

func applyPerformanceSample(bucket *performanceBucket, sample performanceSample) {
	if bucket == nil {
		return
	}

	bucket.tradeCount++
	bucket.sumLogLong += sample.logLong
	bucket.sumLogShort += sample.logShort
	bucket.logLongs = append(bucket.logLongs, sample.logLong)
	bucket.logShorts = append(bucket.logShorts, sample.logShort)
	bucket.sumPositive += sample.positivePnL
	bucket.sumNegative += sample.negativePnL
}

func buildFixedPerformanceBins(buckets map[int]*performanceBucket) []*ScoreBinPerformance {
	if len(buckets) == 0 {
		return nil
	}

	binStarts := make([]int, 0, len(buckets))
	for binStart := range buckets {
		binStarts = append(binStarts, binStart)
	}
	sort.Ints(binStarts)

	bins := make([]*ScoreBinPerformance, 0, len(binStarts))
	for _, binStart := range binStarts {
		bucket := buckets[binStart]
		if bucket == nil || bucket.tradeCount == 0 {
			continue
		}

		profitFactorLong, profitFactorShort := calculateProfitFactors(bucket.sumPositive, bucket.sumNegative)
		bins = append(bins, &ScoreBinPerformance{
			BinStart:                 binStart,
			BinLabel:                 fmt.Sprintf("%d-%d", binStart, binStart+5),
			TradeCount:               bucket.tradeCount,
			ExpectedValueLong:        sanitizeExpectedValue(bucket.sumLogLong / float64(bucket.tradeCount)),
			MedianExpectedValueLong:  sanitizeExpectedValue(medianFloat64(bucket.logLongs)),
			ProfitFactorLong:         profitFactorLong,
			ExpectedValueShort:       sanitizeExpectedValue(bucket.sumLogShort / float64(bucket.tradeCount)),
			MedianExpectedValueShort: sanitizeExpectedValue(medianFloat64(bucket.logShorts)),
			ProfitFactorShort:        profitFactorShort,
		})
	}

	return bins
}

func clampProfitFactor(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return math.Min(value, performanceBinProfitFactorCap)
}

func clampScoreRange(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func calculateProfitFactors(sumPositive, sumNegative float64) (float64, float64) {
	profitFactorLong := 0.0
	profitFactorShort := 0.0

	if sumNegative <= 1e-9 && sumPositive > 0 {
		profitFactorLong = performanceBinProfitFactorCap
	} else if sumNegative > 1e-9 {
		profitFactorLong = clampProfitFactor(sumPositive / sumNegative)
	}

	if sumPositive <= 1e-9 && sumNegative > 0 {
		profitFactorShort = performanceBinProfitFactorCap
	} else if sumPositive > 1e-9 {
		profitFactorShort = clampProfitFactor(sumNegative / sumPositive)
	}

	return clampProfitFactor(profitFactorLong), clampProfitFactor(profitFactorShort)
}

func clampLogReturnBase(value float64) float64 {
	if value <= 1e-9 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 1e-9
	}
	return value
}

func medianFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func sanitizeExpectedValue(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func isFiniteScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
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
		limit = 500
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

func (s *ShadowSnapshotStore) GetLatestBySymbol(traderID, symbol string, includeAllTraders bool) (*ShadowSnapshot, error) {
	if symbol == "" || (!includeAllTraders && traderID == "") {
		return nil, nil
	}

	var snapshot ShadowSnapshot
	query := s.db.Where("symbol = ?", symbol)
	if !includeAllTraders {
		query = query.Where("trader_id = ?", traderID)
	}
	err := query.Order("decision_time DESC, id DESC").First(&snapshot).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest shadow snapshot by symbol: %w", err)
	}
	return &snapshot, nil
}
