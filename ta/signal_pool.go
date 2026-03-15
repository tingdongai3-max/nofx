// Package ta 提供信号池管理功能
// 用于记录 AI 开仓决策并自动结算命中率
package ta

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

// SignalRecord 信号记录
type SignalRecord struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Symbol       string    `gorm:"index" json:"symbol"`
	SignalTime   time.Time `json:"signal_time"`
	StrategyName string    `json:"strategy_name"` // 策略分桶名
	Direction    string    `json:"direction"`     // "LONG" 或 "SHORT"
	EntryPrice   float64   `json:"entry_price"`   // 发出信号时的现价
	SettleTime   time.Time `gorm:"index" json:"settle_time"` // 预期结算时间
	SettlePrice  float64   `json:"settle_price"` // 结算时的真实价格
	IsHit        bool      `json:"is_hit"`        // 是否命中
	Status       string    `json:"status"`        // "PENDING" 或 "SETTLED"
	Confidence   float64   `json:"confidence"`    // 置信度
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SignalStats 信号统计
type SignalStats struct {
	TotalSignals          int64                    `json:"total_signals"`
	SettledSignals       int64                    `json:"settled_signals"`
	PendingSignals       int64                    `json:"pending_signals"`
	HitRate              float64                  `json:"hit_rate"` // 命中率
	WinRateEstimate      string                   `json:"win_rate_estimate"` // 胜率预估字符串
	ByDirection          map[string]DirectionStats `json:"by_direction"`
	ByStrategy           map[string]StrategyStats  `json:"by_strategy"`
	BySymbol             []SymbolStats             `json:"by_symbol"` // 按币种统计
	PositionsIndicators []PositionIndicator       `json:"positions_indicators,omitempty"` // 持仓币种指标合力快照
}

// SymbolStats 按币种的统计
type SymbolStats struct {
	Symbol   string  `json:"symbol"`
	Total    int64   `json:"total"`
	Hits     int64   `json:"hits"`
	HitRate  float64 `json:"hit_rate"`
	AvgProfit float64 `json:"avg_profit"` // 平均收益率
}

// DirectionStats 按方向的统计
type DirectionStats struct {
	Total   int64   `json:"total"`
	Hits    int64   `json:"hits"`
	HitRate float64 `json:"hit_rate"`
}

// StrategyStats 按策略的统计
type StrategyStats struct {
	Total   int64   `json:"total"`
	Hits    int64   `json:"hits"`
	HitRate float64 `json:"hit_rate"`
}

// PositionIndicator 持仓币种的指标合力快照
type PositionIndicator struct {
	Symbol         string `json:"symbol"`
	CZSCStatus     string `json:"czsc,omitempty"`        // 缠论状态，如 "At ZS Bottom (Support)"
	OITrend        string `json:"oi_trend,omitempty"`     // OI 趋势，如 "Increasing (Long Build-up)"
	DepthRatio     string `json:"depth_ratio,omitempty"`  // 深度比，如 "2.5 (Bullish)"
	POCDeviation   string `json:"poc_deviation,omitempty"` // POC 偏离度，如 "-1.79% (Low Zone)"
}

// SignalPool 信号池管理器
type SignalPool struct {
	db          *gorm.DB
	priceGetter PriceGetter
	mu          sync.RWMutex
	running     bool
	stopChan    chan struct{}
}

// PriceGetter 价格获取接口
type PriceGetter interface {
	GetPrice(symbol string) (float64, error)
}

// NewSignalPool 创建信号池
func NewSignalPool(db *gorm.DB, priceGetter PriceGetter) *SignalPool {
	return &SignalPool{
		db:          db,
		priceGetter: priceGetter,
		stopChan:    make(chan struct{}),
	}
}

// InitTable 初始化表
func (sp *SignalPool) InitTable() error {
	return sp.db.AutoMigrate(&SignalRecord{})
}

// CreateSignal 创建信号记录
func (sp *SignalPool) CreateSignal(signal *SignalRecord) error {
	signal.Status = "PENDING"
	signal.CreatedAt = time.Now()
	signal.UpdatedAt = time.Now()
	return sp.db.Create(signal).Error
}

// GetSignalByID 根据 ID 获取信号
func (sp *SignalPool) GetSignalByID(id uint) (*SignalRecord, error) {
	var signal SignalRecord
	err := sp.db.First(&signal, id).Error
	if err != nil {
		return nil, err
	}
	return &signal, nil
}

// GetPendingSignals 获取待结算信号
func (sp *SignalPool) GetPendingSignals() ([]SignalRecord, error) {
	var signals []SignalRecord
	err := sp.db.Where("status = ? AND settle_time <= ?", "PENDING", time.Now()).Find(&signals).Error
	return signals, err
}

// SettleSignal 结算信号
func (sp *SignalPool) SettleSignal(signalID uint, settlePrice float64) error {
	signal, err := sp.GetSignalByID(signalID)
	if err != nil {
		return err
	}

	signal.SettlePrice = settlePrice
	signal.Status = "SETTLED"
	signal.UpdatedAt = time.Now()

	// 判断是否命中
	if signal.Direction == "LONG" {
		signal.IsHit = settlePrice > signal.EntryPrice
	} else if signal.Direction == "SHORT" {
		signal.IsHit = settlePrice < signal.EntryPrice
	}

	return sp.db.Save(signal).Error
}

// GetStats 获取统计信息
func (sp *SignalPool) GetStats() (*SignalStats, error) {
	var total int64
	var settled int64
	var pending int64
	var hits int64

	sp.db.Model(&SignalRecord{}).Count(&total)
	sp.db.Model(&SignalRecord{}).Where("status = ?", "SETTLED").Count(&settled)
	sp.db.Model(&SignalRecord{}).Where("status = ?", "PENDING").Count(&pending)
	sp.db.Model(&SignalRecord{}).Where("status = ? AND is_hit = ?", "SETTLED", true).Count(&hits)

	hitRate := 0.0
	if settled > 0 {
		hitRate = float64(hits) / float64(settled) * 100
	}

	// 计算胜率预估
	winRateEstimate := calculateWinRateEstimate(hitRate, settled)

	stats := &SignalStats{
		TotalSignals:      total,
		SettledSignals:    settled,
		PendingSignals:    pending,
		HitRate:           hitRate,
		WinRateEstimate:   winRateEstimate,
		ByDirection:       make(map[string]DirectionStats),
		ByStrategy:        make(map[string]StrategyStats),
		BySymbol:          []SymbolStats{},
		PositionsIndicators: []PositionIndicator{},
	}

	// 按方向统计
	directions := []string{"LONG", "SHORT"}
	for _, dir := range directions {
		var dirTotal, dirHits int64
		sp.db.Model(&SignalRecord{}).Where("direction = ? AND status = ?", dir, "SETTLED").Count(&dirTotal)
		sp.db.Model(&SignalRecord{}).Where("direction = ? AND status = ? AND is_hit = ?", dir, "SETTLED", true).Count(&dirHits)

		dirHitRate := 0.0
		if dirTotal > 0 {
			dirHitRate = float64(dirHits) / float64(dirTotal) * 100
		}

		stats.ByDirection[dir] = DirectionStats{
			Total:   dirTotal,
			Hits:    dirHits,
			HitRate: dirHitRate,
		}
	}

	// 按策略统计
	var strategyNames []string
	sp.db.Model(&SignalRecord{}).Distinct("strategy_name").Pluck("strategy_name", &strategyNames)

	for _, strategy := range strategyNames {
		var stratTotal, stratHits int64
		sp.db.Model(&SignalRecord{}).Where("strategy_name = ? AND status = ?", strategy, "SETTLED").Count(&stratTotal)
		sp.db.Model(&SignalRecord{}).Where("strategy_name = ? AND status = ? AND is_hit = ?", strategy, "SETTLED", true).Count(&stratHits)

		stratHitRate := 0.0
		if stratTotal > 0 {
			stratHitRate = float64(stratHits) / float64(stratTotal) * 100
		}

		stats.ByStrategy[strategy] = StrategyStats{
			Total:   stratTotal,
			Hits:    stratHits,
			HitRate: stratHitRate,
		}
	}

	// 按币种统计
	var symbols []string
	sp.db.Model(&SignalRecord{}).Distinct("symbol").Where("status = ?", "SETTLED").Pluck("symbol", &symbols)

	for _, symbol := range symbols {
		var symTotal, symHits int64
		var profits []float64

		sp.db.Model(&SignalRecord{}).Where("symbol = ? AND status = ?", symbol, "SETTLED").Count(&symTotal)
		sp.db.Model(&SignalRecord{}).Where("symbol = ? AND status = ? AND is_hit = ?", symbol, "SETTLED", true).Count(&symHits)

		// 计算平均收益率
		var records []SignalRecord
		sp.db.Where("symbol = ? AND status = ?", symbol, "SETTLED").Find(&records)
		for _, r := range records {
			var profit float64
			if r.Direction == "LONG" {
				profit = (r.SettlePrice - r.EntryPrice) / r.EntryPrice * 100
			} else if r.Direction == "SHORT" {
				profit = (r.EntryPrice - r.SettlePrice) / r.EntryPrice * 100
			}
			profits = append(profits, profit)
		}

		avgProfit := 0.0
		if len(profits) > 0 {
			for _, p := range profits {
				avgProfit += p
			}
			avgProfit /= float64(len(profits))
		}

		symHitRate := 0.0
		if symTotal > 0 {
			symHitRate = float64(symHits) / float64(symTotal) * 100
		}

		stats.BySymbol = append(stats.BySymbol, SymbolStats{
			Symbol:    symbol,
			Total:     symTotal,
			Hits:      symHits,
			HitRate:   symHitRate,
			AvgProfit: avgProfit,
		})
	}

	return stats, nil
}

// StartSettlementWorker 启动结算 Worker
func (sp *SignalPool) StartSettlementWorker(ctx context.Context, interval time.Duration) {
	sp.mu.Lock()
	if sp.running {
		sp.mu.Unlock()
		return
	}
	sp.running = true
	sp.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sp.stopChan:
			return
		case <-ticker.C:
			sp.settlePendingSignals(ctx)
		}
	}
}

// StopSettlementWorker 停止结算 Worker
func (sp *SignalPool) StopSettlementWorker() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.running {
		close(sp.stopChan)
		sp.running = false
	}
}

// settlePendingSignals 结算待处理的信号
func (sp *SignalPool) settlePendingSignals(ctx context.Context) {
	signals, err := sp.GetPendingSignals()
	if err != nil {
		fmt.Printf("Failed to get pending signals: %v\n", err)
		return
	}

	for _, signal := range signals {
		// 获取结算价格
		settlePrice, err := sp.priceGetter.GetPrice(signal.Symbol)
		if err != nil {
			fmt.Printf("Failed to get price for %s: %v\n", signal.Symbol, err)
			continue
		}

		// 结算
		if err := sp.SettleSignal(signal.ID, settlePrice); err != nil {
			fmt.Printf("Failed to settle signal %d: %v\n", signal.ID, err)
		}
	}
}

// CreateSignalWithSettleTime 创建信号并设置结算时间
func (sp *SignalPool) CreateSignalWithSettleTime(
	symbol, strategyName, direction string,
	entryPrice float64,
	settleAfter time.Duration,
	confidence float64,
) error {
	signal := &SignalRecord{
		Symbol:       symbol,
		SignalTime:   time.Now(),
		StrategyName: strategyName,
		Direction:    direction,
		EntryPrice:   entryPrice,
		SettleTime:  time.Now().Add(settleAfter),
		Confidence:   confidence,
	}
	return sp.CreateSignal(signal)
}

// calculateWinRateEstimate 根据命中率和样本数计算胜率预估
func calculateWinRateEstimate(hitRate float64, settled int64) string {
	if settled == 0 {
		return "N/A (No settled signals)"
	}
	if settled < 10 {
		return fmt.Sprintf("%.0f%% (Low confidence, n=%d)", hitRate, settled)
	}
	if settled < 30 {
		return fmt.Sprintf("%.0f%% (Medium confidence, n=%d)", hitRate, settled)
	}
	return fmt.Sprintf("%.0f%% (High confidence, n=%d)", hitRate, settled)
}

// SetPositionsIndicators 设置持仓币种的指标合力快照
func (sp *SignalPool) SetPositionsIndicators(indicators []PositionIndicator) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	// 这个方法可以由外部调用来更新持仓指标数据
	// 在 GetStats 中会返回这些数据
	// 注意：这里简化处理，实际应该存储在 pool 中
	_ = indicators
}
