package trader

import (
	"strings"
	"testing"

	"nofx/store"
)

func TestRuntimeCapabilityResolver_DoesNotExposeUnsupportedPhaseOneActions(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-1",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-1",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	for _, blocked := range []string{"add_position", "move_stop_loss", "set_trailing_protection"} {
		if !containsString(preview.BlockedActions, blocked) {
			t.Fatalf("expected %s to be blocked", blocked)
		}
		if containsString(preview.AllowedActions, blocked) {
			t.Fatalf("did not expect %s to be allowed", blocked)
		}
	}
}

func TestRuntimeCapabilityResolver_BlocksOpenWhenPositionExists(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-1",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-1",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-1",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if containsString(preview.AllowedActions, "open_long") || containsString(preview.AllowedActions, "open_short") {
		t.Fatalf("did not expect open actions to be allowed when position exists")
	}
	if !containsString(preview.AllowedActions, "close_position") {
		t.Fatalf("expected close_position to be allowed when position exists")
	}
}

func TestRuntimeCapabilityResolver_BlocksByOrderState(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-1",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		HasStateMismatch:    true,
		HasWorkingOrders:    true,
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-1",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-1",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if !containsString(preview.BlockedActions, "close_position") {
		t.Fatalf("expected close_position to be blocked by order state")
	}
	if !containsCategory(preview.BlockReasons, "order_state") {
		t.Fatalf("expected order_state block reason")
	}
}

func TestRuntimeCapabilityResolver_BlocksSetProtectionWhenProtectionGroupIsNonTerminal(t *testing.T) {
	resolver := NewRuntimeCapabilityResolver()
	preview, err := resolver.Resolve(&RuntimeCapabilityRequest{
		TraderID:            "trader-1",
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		HasProtection:       true,
		HasProtectionOrders: true,
		ProtectionGroupStatus: "pending_attach",
		StopLossArmed:       true,
		TakeProfitArmed:     true,
		ProtectionConsistency: "pending",
		StrategyProfile: &store.StrategyProfile{
			TraderID:         "trader-1",
			Exchange:         "binance_usdm",
			Mode:             "one_way",
			ExecutionEnabled: true,
		},
		PositionAggregate: &store.PositionAggregate{
			TraderID:          "trader-1",
			Symbol:            "BTCUSDT",
			Side:              "LONG",
			TotalQty:          1,
			AvailableQty:      1,
			ExecutionEligible: true,
		},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if containsString(preview.AllowedActions, "set_protection") {
		t.Fatalf("did not expect set_protection to be allowed for a non-terminal protection group")
	}
	if !containsString(preview.BlockedActions, "set_protection") {
		t.Fatalf("expected set_protection to be blocked for a non-terminal protection group")
	}
	if !containsBlockedReason(preview.BlockReasons, "set_protection", "protection", "non-terminal") {
		t.Fatalf("expected a non-terminal protection block reason, got %#v", preview.BlockReasons)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsCategory(reasons []CapabilityReason, target string) bool {
	for _, reason := range reasons {
		if reason.Category == target {
			return true
		}
	}
	return false
}

func containsBlockedReason(reasons []CapabilityReason, action, category, reasonFragment string) bool {
	for _, reason := range reasons {
		if reason.Action == action && reason.Category == category && strings.Contains(reason.Reason, reasonFragment) {
			return true
		}
	}
	return false
}
