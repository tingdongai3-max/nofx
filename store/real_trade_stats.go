package store

import (
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
)

const (
	RealTradeResonanceStatusOpen   = "OPEN"
	RealTradeResonanceStatusClosed = "CLOSED"
)

type RealTradeResonanceRecord struct {
	ID                int64   `gorm:"primaryKey;autoIncrement" json:"id"`
	TraderID          string  `gorm:"column:trader_id;not null;index:idx_real_trade_resonance_trader_status,priority:1;index:idx_real_trade_resonance_trader_exit,priority:1;uniqueIndex:uidx_real_trade_resonance_order,priority:1" json:"trader_id"`
	ExchangeID        string  `gorm:"column:exchange_id;not null;default:'';index:idx_real_trade_resonance_exchange;uniqueIndex:uidx_real_trade_resonance_order,priority:2" json:"exchange_id"`
	OrderID           string  `gorm:"column:order_id;not null;default:'';uniqueIndex:uidx_real_trade_resonance_order,priority:3" json:"order_id"`
	Symbol            string  `gorm:"column:symbol;not null;index:idx_real_trade_resonance_symbol" json:"symbol"`
	Side              string  `gorm:"column:side;not null;default:''" json:"side"`
	EntryTime         int64   `gorm:"column:entry_time;not null;index:idx_real_trade_resonance_entry" json:"entry_time"`
	EntryGlobalEV     float64 `gorm:"column:entry_global_ev;default:0" json:"entry_global_ev"`
	EntrySectorEV     float64 `gorm:"column:entry_sector_ev;default:0" json:"entry_sector_ev"`
	EntrySymbolEV     float64 `gorm:"column:entry_symbol_ev;default:0" json:"entry_symbol_ev"`
	HoldSampleCount   int     `gorm:"column:hold_sample_count;default:0" json:"hold_sample_count"`
	HoldSumGlobalEV   float64 `gorm:"column:hold_sum_global_ev;default:0" json:"hold_sum_global_ev"`
	HoldSumSectorEV   float64 `gorm:"column:hold_sum_sector_ev;default:0" json:"hold_sum_sector_ev"`
	HoldSumSymbolEV   float64 `gorm:"column:hold_sum_symbol_ev;default:0" json:"hold_sum_symbol_ev"`
	HoldAvgGlobalEV   float64 `gorm:"column:hold_avg_global_ev;default:0" json:"hold_avg_global_ev"`
	HoldAvgSectorEV   float64 `gorm:"column:hold_avg_sector_ev;default:0" json:"hold_avg_sector_ev"`
	HoldAvgSymbolEV   float64 `gorm:"column:hold_avg_symbol_ev;default:0" json:"hold_avg_symbol_ev"`
	HoldRetentionRate float64 `gorm:"column:hold_retention_rate;default:0" json:"hold_retention_rate"`
	FinalPnL          float64 `gorm:"column:final_pnl;default:0" json:"final_pnl"`
	ExitTime          int64   `gorm:"column:exit_time;default:0;index:idx_real_trade_resonance_trader_exit,priority:2" json:"exit_time"`
	LastSampleAt      int64   `gorm:"column:last_sample_at;default:0" json:"last_sample_at"`
	Status            string  `gorm:"column:status;not null;default:OPEN;index:idx_real_trade_resonance_trader_status,priority:2" json:"status"`
	CreatedAt         int64   `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt         int64   `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (RealTradeResonanceRecord) TableName() string {
	return "real_trade_resonance_records"
}

type RealTradeResonanceSample struct {
	GlobalEV float64
	SectorEV float64
	SymbolEV float64
}

type RealTradeStatsStore struct {
	db *gorm.DB
}

func NewRealTradeStatsStore(db *gorm.DB) *RealTradeStatsStore {
	return &RealTradeStatsStore{db: db}
}

func (s *RealTradeStatsStore) InitTables() error {
	if err := s.db.AutoMigrate(&RealTradeResonanceRecord{}); err != nil {
		return fmt.Errorf("failed to migrate real_trade_resonance_records: %w", err)
	}
	return nil
}

func (s *RealTradeStatsStore) CreateOpenRecord(record *RealTradeResonanceRecord) error {
	if record == nil {
		return nil
	}
	nowMs := time.Now().UTC().UnixMilli()
	if record.CreatedAt == 0 {
		record.CreatedAt = nowMs
	}
	if record.UpdatedAt == 0 {
		record.UpdatedAt = record.CreatedAt
	}
	if record.EntryTime == 0 {
		record.EntryTime = record.CreatedAt
	}
	if record.Status == "" {
		record.Status = RealTradeResonanceStatusOpen
	}
	if record.HoldSampleCount <= 0 {
		record.HoldSampleCount = 1
		record.HoldSumGlobalEV = record.EntryGlobalEV
		record.HoldSumSectorEV = record.EntrySectorEV
		record.HoldSumSymbolEV = record.EntrySymbolEV
		record.HoldAvgGlobalEV = record.EntryGlobalEV
		record.HoldAvgSectorEV = record.EntrySectorEV
		record.HoldAvgSymbolEV = record.EntrySymbolEV
		record.LastSampleAt = record.EntryTime
		record.HoldRetentionRate = computeResonanceRetentionRate(
			RealTradeResonanceSample{
				GlobalEV: record.EntryGlobalEV,
				SectorEV: record.EntrySectorEV,
				SymbolEV: record.EntrySymbolEV,
			},
			RealTradeResonanceSample{
				GlobalEV: record.HoldAvgGlobalEV,
				SectorEV: record.HoldAvgSectorEV,
				SymbolEV: record.HoldAvgSymbolEV,
			},
		)
	}

	return s.db.Where(
		"trader_id = ? AND exchange_id = ? AND order_id = ?",
		record.TraderID,
		record.ExchangeID,
		record.OrderID,
	).Assign(record).FirstOrCreate(record).Error
}

func (s *RealTradeStatsStore) GetByOrderID(traderID, exchangeID, orderID string) (*RealTradeResonanceRecord, error) {
	if traderID == "" || orderID == "" {
		return nil, nil
	}

	var record RealTradeResonanceRecord
	err := s.db.Where(
		"trader_id = ? AND exchange_id = ? AND order_id = ?",
		traderID,
		exchangeID,
		orderID,
	).First(&record).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("load real_trade_resonance_record by order id: %w", err)
	}
	return &record, nil
}

func (s *RealTradeStatsStore) ApplyHoldSampleByOrderID(traderID, exchangeID, orderID string, sample RealTradeResonanceSample, sampleTimeMs int64) error {
	if traderID == "" || orderID == "" {
		return nil
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var record RealTradeResonanceRecord
		err := tx.Where(
			"trader_id = ? AND exchange_id = ? AND order_id = ? AND status = ?",
			traderID,
			exchangeID,
			orderID,
			RealTradeResonanceStatusOpen,
		).First(&record).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return fmt.Errorf("load open real_trade_resonance_record: %w", err)
		}

		record.HoldSampleCount++
		record.HoldSumGlobalEV += sample.GlobalEV
		record.HoldSumSectorEV += sample.SectorEV
		record.HoldSumSymbolEV += sample.SymbolEV
		record.HoldAvgGlobalEV = record.HoldSumGlobalEV / float64(record.HoldSampleCount)
		record.HoldAvgSectorEV = record.HoldSumSectorEV / float64(record.HoldSampleCount)
		record.HoldAvgSymbolEV = record.HoldSumSymbolEV / float64(record.HoldSampleCount)
		record.HoldRetentionRate = computeResonanceRetentionRate(
			RealTradeResonanceSample{
				GlobalEV: record.EntryGlobalEV,
				SectorEV: record.EntrySectorEV,
				SymbolEV: record.EntrySymbolEV,
			},
			RealTradeResonanceSample{
				GlobalEV: record.HoldAvgGlobalEV,
				SectorEV: record.HoldAvgSectorEV,
				SymbolEV: record.HoldAvgSymbolEV,
			},
		)
		record.LastSampleAt = sampleTimeMs
		record.UpdatedAt = time.Now().UTC().UnixMilli()

		return tx.Model(&RealTradeResonanceRecord{}).Where("id = ?", record.ID).Updates(map[string]interface{}{
			"hold_sample_count":   record.HoldSampleCount,
			"hold_sum_global_ev":  record.HoldSumGlobalEV,
			"hold_sum_sector_ev":  record.HoldSumSectorEV,
			"hold_sum_symbol_ev":  record.HoldSumSymbolEV,
			"hold_avg_global_ev":  record.HoldAvgGlobalEV,
			"hold_avg_sector_ev":  record.HoldAvgSectorEV,
			"hold_avg_symbol_ev":  record.HoldAvgSymbolEV,
			"hold_retention_rate": record.HoldRetentionRate,
			"last_sample_at":      record.LastSampleAt,
			"updated_at":          record.UpdatedAt,
		}).Error
	})
}

func (s *RealTradeStatsStore) CloseRecordByOrderID(traderID, exchangeID, orderID string, finalPnL float64, exitTimeMs int64) error {
	if traderID == "" || orderID == "" {
		return nil
	}

	updates := map[string]interface{}{
		"final_pnl":  finalPnL,
		"exit_time":  exitTimeMs,
		"status":     RealTradeResonanceStatusClosed,
		"updated_at": time.Now().UTC().UnixMilli(),
	}
	return s.db.Model(&RealTradeResonanceRecord{}).
		Where("trader_id = ? AND exchange_id = ? AND order_id = ? AND status = ?", traderID, exchangeID, orderID, RealTradeResonanceStatusOpen).
		Updates(updates).Error
}

func (s *RealTradeStatsStore) ListClosedByTrader(traderID string, limit int) ([]*RealTradeResonanceRecord, error) {
	var rows []*RealTradeResonanceRecord
	query := s.db.Where("trader_id = ? AND status = ?", traderID, RealTradeResonanceStatusClosed).
		Order("exit_time DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query closed real_trade_resonance_records: %w", err)
	}
	return rows, nil
}

func computeResonanceRetentionRate(entry, hold RealTradeResonanceSample) float64 {
	entryStrength := resonanceStrength(entry)
	if entryStrength <= 0 {
		return 0
	}
	return resonanceStrength(hold) / entryStrength
}

func resonanceStrength(sample RealTradeResonanceSample) float64 {
	return (math.Abs(sample.GlobalEV) + math.Abs(sample.SectorEV) + math.Abs(sample.SymbolEV)) / 3
}
