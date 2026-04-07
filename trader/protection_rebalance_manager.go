package trader

import (
	"fmt"
	"math"

	"nofx/store"
)

// ProtectionRebalanceManager keeps the fixed-protection truth layer aligned with the current remaining position size.
// It only adjusts quantity and never changes the protection price geometry.
type ProtectionRebalanceManager struct {
	store        *store.Store
	fixedManager *FixedProtectionManager
}

// NewProtectionRebalanceManager creates a new protection rebalance manager.
func NewProtectionRebalanceManager(st *store.Store, fixedManager *FixedProtectionManager) *ProtectionRebalanceManager {
	return &ProtectionRebalanceManager{
		store:        st,
		fixedManager: fixedManager,
	}
}

// RebalanceForAggregate refreshes the fixed protection quantity to match a position aggregate.
// It is a truth-layer coordination entrypoint, not a prompt-layer action.
func (m *ProtectionRebalanceManager) RebalanceForAggregate(aggregate *store.PositionAggregate) (*store.ProtectionGroup, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("protection rebalance manager store is not configured")
	}
	if aggregate == nil {
		return nil, fmt.Errorf("position aggregate is required")
	}
	if aggregate.TotalQty <= 0 {
		return nil, nil
	}
	if m.fixedManager == nil {
		return nil, fmt.Errorf("fixed protection manager is not configured")
	}

	profile, err := m.store.StrategyProfile().GetByTraderID(aggregate.TraderID)
	if err != nil {
		return nil, err
	}

	summary, err := m.store.ProtectionGroup().SummarizeForTraderSymbolSide(aggregate.TraderID, aggregate.Symbol, aggregate.Side)
	if err != nil {
		return nil, err
	}
	if summary == nil || !summary.HasProtection || summary.ProtectionGroupStatus == "closed" {
		return m.fixedManager.EnsureFixedProtectionForAggregate(aggregate, profile)
	}
	if math.Abs(summary.ProtectedQuantity-aggregate.TotalQty) < 0.000001 && summary.ProtectionGroupStatus == "armed" {
		return m.store.ProtectionGroup().GetByKeys(aggregate.TraderID, summary.ProtectionGroupID, summary.LinkedPositionKey)
	}

	return m.fixedManager.RebalanceFixedProtectionForAggregate(aggregate, profile)
}

