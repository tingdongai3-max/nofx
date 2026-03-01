package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PendingReasoning 实盘开仓时暂存的 AI 思维链，等 OrderSync 创建 TraderPosition 时按 Symbol+Side 匹配并填入 ai_reasoning_at_open
type PendingReasoning struct {
	ID        int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	TraderID  string `gorm:"column:trader_id;not null;index:idx_pending_trader_symbol_side" json:"trader_id"`
	Symbol    string `gorm:"column:symbol;not null;index:idx_pending_trader_symbol_side" json:"symbol"`
	Side      string `gorm:"column:side;not null;index:idx_pending_trader_symbol_side" json:"side"` // LONG / SHORT
	Reasoning string `gorm:"column:reasoning;type:text" json:"reasoning"`
	CreatedAt int64  `gorm:"column:created_at;not null" json:"created_at"` // Unix ms UTC
}

// TableName returns the table name
func (PendingReasoning) TableName() string {
	return "pending_reasonings"
}

// AddPendingReasoning 实盘下单成功后调用：将本轮 CoT 写入 pending，供 OrderSync 创建仓位时填充
func (s *PositionStore) AddPendingReasoning(traderID, symbol, side, reasoning string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToUpper(strings.TrimSpace(side))
	if side != "LONG" && side != "SHORT" {
		return fmt.Errorf("invalid side for pending reasoning: %q", side)
	}
	nowMs := time.Now().UTC().UnixMilli()
	r := &PendingReasoning{
		TraderID:  traderID,
		Symbol:    symbol,
		Side:      side,
		Reasoning: reasoning,
		CreatedAt: nowMs,
	}
	return s.db.Create(r).Error
}

// TakeLatestPendingReasoning 取出并删除最近一条匹配的 pending reasoning（OrderSync 创建新仓位时调用）
// 返回 reasoning 文本；若无匹配或已取完则返回 ""，不报错
func (s *PositionStore) TakeLatestPendingReasoning(traderID, symbol, side string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToUpper(strings.TrimSpace(side))
	var r PendingReasoning
	err := s.db.Where("trader_id = ? AND symbol = ? AND side = ?", traderID, symbol, side).
		Order("created_at DESC").
		First(&r).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	if err := s.db.Delete(&r).Error; err != nil {
		return "", fmt.Errorf("delete pending reasoning: %w", err)
	}
	return r.Reasoning, nil
}
