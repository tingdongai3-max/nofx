package store

import (
	"fmt"
	"strings"
	"time"
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

// normalizeSymbolForMatch 将 -, _, / 全部去掉后 ToUpper，用于 Symbol 模糊匹配（PIPPIN-USDT-SWAP 与 PIPPINUSDT 可匹配）
func normalizeSymbolForMatch(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "/", "")
	return s
}

// TakeLatestPendingReasoning 取出并删除最近一条匹配的 pending reasoning（OrderSync 创建新仓位时调用）
// 支持 Symbol 模糊匹配：去除 -, _, / 后再 ToUpper 对比，确保 PIPPIN-USDT-SWAP 与 PIPPINUSDT 能匹配
// 返回 reasoning 文本；若无匹配或已取完则返回 ""，不报错
func (s *PositionStore) TakeLatestPendingReasoning(traderID, symbol, side string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToUpper(strings.TrimSpace(side))
	targetNorm := normalizeSymbolForMatch(symbol)
	var candidates []PendingReasoning
	err := s.db.Where("trader_id = ? AND side = ?", traderID, side).
		Order("created_at DESC").
		Find(&candidates).Error
	if err != nil {
		return "", err
	}
	for _, r := range candidates {
		if normalizeSymbolForMatch(r.Symbol) == targetNorm {
			if err := s.db.Delete(&r).Error; err != nil {
				return "", fmt.Errorf("delete pending reasoning: %w", err)
			}
			return r.Reasoning, nil
		}
	}
	return "", nil
}

// RemoveLatestPendingReasoning removes the most recent pending reasoning for a symbol+side pair.
func (s *PositionStore) RemoveLatestPendingReasoning(traderID, symbol, side string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	side = strings.ToUpper(strings.TrimSpace(side))
	targetNorm := normalizeSymbolForMatch(symbol)
	var candidates []PendingReasoning
	err := s.db.Where("trader_id = ? AND side = ?", traderID, side).
		Order("created_at DESC").
		Find(&candidates).Error
	if err != nil {
		return err
	}
	for _, r := range candidates {
		if normalizeSymbolForMatch(r.Symbol) == targetNorm {
			return s.db.Delete(&r).Error
		}
	}
	return nil
}

// ClearPendingReasonings deletes all outstanding pending reasonings for a trader.
func (s *PositionStore) ClearPendingReasonings(traderID string) error {
	return s.db.Where("trader_id = ?", traderID).Delete(&PendingReasoning{}).Error
}
