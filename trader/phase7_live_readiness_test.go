package trader

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"nofx/store"
	tradertypes "nofx/trader/types"
)

func seedPhase7RuntimeIdentity(t *testing.T, st *store.Store, userID, traderID string) string {
	t.Helper()

	if err := st.User().Create(&store.User{
		ID:           userID,
		Email:        userID + "@example.com",
		PasswordHash: "phase7-test-hash",
	}); err != nil && !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("seed user failed: %v", err)
	}

	aiModelID := userID + "_minimax"
	if err := st.AIModel().Create(userID, aiModelID, "MiniMax", "minimax", true, "test-key", "https://example.com/anthropic"); err != nil {
		t.Fatalf("seed ai model failed: %v", err)
	}

	exchangeID, err := st.Exchange().Create(
		userID,
		"binance",
		"Default",
		true,
		"test-api-key",
		"test-secret",
		"",
		false,
		"",
		true,
		"",
		"",
		"",
		"",
		"",
		"",
		0,
	)
	if err != nil {
		t.Fatalf("seed exchange failed: %v", err)
	}

	if err := st.Trader().Create(&store.Trader{
		ID:                  traderID,
		UserID:              userID,
		Name:                "Phase7 Validation Trader",
		AIModelID:           aiModelID,
		ExchangeID:          exchangeID,
		InitialBalance:      1000,
		ScanIntervalMinutes: 3,
		IsRunning:           true,
		IsCrossMargin:       true,
		ShowInCompetition:   true,
	}); err != nil {
		t.Fatalf("seed trader failed: %v", err)
	}

	return userID
}

func seedPhase7ScaleOutProtectedScope(t *testing.T, st *store.Store, traderID, symbol, side string) (*store.ScaleOutPlan, []*store.ScaleOutPlanLevel) {
	t.Helper()

	seedPhase6StrategyAndPosition(t, st, traderID, symbol, side, true, true, true, true, 0.8, 65000)

	stopIntent, takeIntent, stopExchange, takeExchange := seedPhase6DynamicProtectionState(
		t,
		st,
		traderID,
		symbol,
		side,
		"pg-phase7-"+strings.ToLower(symbol),
		"fixed",
		"armed",
		64000,
		64000,
		68000,
		68000,
		0.8,
		false,
		false,
		"",
		0,
		1,
	)
	seedPhase6ProtectionOrders(t, st, traderID, symbol, side, "pg-phase7-"+strings.ToLower(symbol), stopIntent, takeIntent, stopExchange, takeExchange, 0.8)

	now := time.Now().UTC()
	planID := "so-phase7-" + strings.ToLower(symbol)
	plan, err := st.ScaleOutPlanBuilder().ApplyScaleOutEvent(&store.ScaleOutPlanEventInput{
		TraderID:            traderID,
		Symbol:              symbol,
		Side:                side,
		LinkedPositionKey:   symbol + "|" + side,
		ScaleOutPlanID:      planID,
		PlanMode:            "fixed_ratio",
		Status:              "partially_filled",
		TotalPlannedQty:     0.4,
		RemainingPlannedQty: 0.2,
		ExecutedQty:         0.2,
		Source:              "phase7_fixture",
		EventType:           "SCALE_OUT_PLAN_ADVANCED",
		EventSource:         "phase7_fixture",
		PayloadJSON:         `{"event":"scale_out_plan_advanced","source":"phase7_fixture"}`,
		EventTime:           now,
		Levels: []*store.ScaleOutPlanLevelInput{
			{
				TraderID:              traderID,
				Symbol:                symbol,
				Side:                  side,
				LinkedPositionKey:     symbol + "|" + side,
				ScaleOutPlanID:        planID,
				LevelIndex:            1,
				TargetType:            "limit_price",
				TargetPrice:           67500,
				PlannedQty:            0.2,
				ExecutedQty:           0.2,
				RemainingQty:          0,
				LinkedOrderIntentID:   planID + "-l1",
				LinkedExchangeOrderID: "7101",
				Status:                "filled",
			},
			{
				TraderID:              traderID,
				Symbol:                symbol,
				Side:                  side,
				LinkedPositionKey:     symbol + "|" + side,
				ScaleOutPlanID:        planID,
				LevelIndex:            2,
				TargetType:            "limit_price",
				TargetPrice:           68500,
				PlannedQty:            0.2,
				ExecutedQty:           0,
				RemainingQty:          0.2,
				LinkedOrderIntentID:   planID + "-l2",
				LinkedExchangeOrderID: "7102",
				Status:                "armed",
			},
		},
	})
	if err != nil {
		t.Fatalf("seed scale-out plan failed: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected scale-out plan")
	}

	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "reduce",
		LocalIntentID:     planID + "-l1",
		LinkedGroupID:     planID,
		LinkedPositionKey: symbol + "|" + side,
		ExchangeOrderID:   "7101",
		ClientOrderID:     planID + "-l1-client",
		OrderType:         "LIMIT",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     false,
		OrigQty:           0.2,
		ExecutedQty:       0.2,
		AvgPrice:          67500,
		Status:            "FILLED",
		Source:            "phase7_fixture",
		EventType:         "ORDER_TRADE_UPDATE",
		EventSource:       "phase7_fixture",
		PayloadJSON:       `{"event":"scale_out_level_filled","level_index":1}`,
		EventTime:         now,
	}); err != nil {
		t.Fatalf("seed first scale-out order failed: %v", err)
	}
	if _, err := st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
		TraderID:          traderID,
		Symbol:            symbol,
		Side:              "SELL",
		PositionSideMode:  "one_way",
		OrderRole:         "reduce",
		LocalIntentID:     planID + "-l2",
		LinkedGroupID:     planID,
		LinkedPositionKey: symbol + "|" + side,
		ExchangeOrderID:   "7102",
		ClientOrderID:     planID + "-l2-client",
		OrderType:         "LIMIT",
		TimeInForce:       "GTC",
		ReduceOnly:        true,
		ClosePosition:     false,
		OrigQty:           0.2,
		ExecutedQty:       0,
		AvgPrice:          68500,
		Status:            "NEW",
		Source:            "phase7_fixture",
		EventType:         "ORDER_NEW",
		EventSource:       "phase7_fixture",
		PayloadJSON:       `{"event":"scale_out_level_armed","level_index":2}`,
		EventTime:         now.Add(time.Second),
	}); err != nil {
		t.Fatalf("seed second scale-out order failed: %v", err)
	}

	if _, err := store.NewPositionAggregateBuilder(st).BuildForTrader(traderID, nil, nil); err != nil {
		t.Fatalf("refresh position aggregate failed: %v", err)
	}

	levels, err := st.ScaleOutPlanLevel().ListByPlanID(planID)
	if err != nil {
		t.Fatalf("load scale-out levels failed: %v", err)
	}
	return plan, levels
}

func buildPhase7WorkingOpenOrders(t *testing.T, st *store.Store, traderID, symbol string) []tradertypes.OpenOrder {
	t.Helper()

	rows, err := st.OrderRegistry().ListByTraderSymbol(traderID, symbol)
	if err != nil {
		t.Fatalf("list order registry rows failed: %v", err)
	}

	orders := make([]tradertypes.OpenOrder, 0)
	for _, row := range rows {
		if row == nil || !row.IsWorking || row.ExchangeOrderID == "" {
			continue
		}
		openOrder := tradertypes.OpenOrder{
			OrderID:      row.ExchangeOrderID,
			Symbol:       row.Symbol,
			Side:         phase7ExchangeSideForRegistryRow(row),
			PositionSide: row.Side,
			Type:         row.OrderType,
			Price:        row.AvgPrice,
			StopPrice:    row.TriggerPrice,
			Quantity:     row.RemainingQty,
			Status:       row.Status,
		}
		if openOrder.Price <= 0 && strings.EqualFold(row.OrderType, "LIMIT") {
			openOrder.Price = row.TriggerPrice
		}
		orders = append(orders, openOrder)
	}
	return orders
}

func phase7ExchangeSideForRegistryRow(row *store.OrderRegistry) string {
	if row == nil {
		return ""
	}
	switch strings.ToUpper(strings.TrimSpace(row.Side)) {
	case "LONG":
		if row.ReduceOnly || row.ClosePosition || strings.Contains(strings.ToUpper(strings.TrimSpace(row.OrderType)), "STOP") || strings.Contains(strings.ToUpper(strings.TrimSpace(row.OrderType)), "TAKE_PROFIT") {
			return "SELL"
		}
		return "BUY"
	case "SHORT":
		if row.ReduceOnly || row.ClosePosition || strings.Contains(strings.ToUpper(strings.TrimSpace(row.OrderType)), "STOP") || strings.Contains(strings.ToUpper(strings.TrimSpace(row.OrderType)), "TAKE_PROFIT") {
			return "BUY"
		}
		return "SELL"
	default:
		return "SELL"
	}
}

func mustPhase7ReplayPayload(t *testing.T, payload map[string]interface{}) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal replay payload failed: %v", err)
	}
	return json.RawMessage(data)
}

func newPhase7ValidationFixtureStore(t *testing.T) *store.Store {
	t.Helper()

	dir := t.TempDir()
	dbPath := dir + "/phase7-validation-fixtures.db"
	t.Setenv("PHASE7_VALIDATION_DB_PATH", dbPath)
	st, err := store.NewWithConfig(store.DBConfig{Type: store.DBTypeSQLite, Path: dbPath})
	if err != nil {
		t.Fatalf("open validation fixture store failed: %v", err)
	}
	return st
}

func seedPhase7ReplayFixture(t *testing.T, fixtureStore *store.Store, fixtureID, traderID, selectedSymbol string, openOrders []tradertypes.OpenOrder, events []replayFixtureEventPayload) {
	t.Helper()
	if fixtureStore == nil {
		t.Fatalf("validation fixture store is required")
	}

	payload, err := json.Marshal(replayFixturePayload{
		TraderID:            traderID,
		SelectedSymbol:      selectedSymbol,
		ExecutionMode:       "live",
		AllowOrderPlacement: true,
		UserStreamReady:     true,
		ExchangeOpenOrders:  openOrders,
		Events:              events,
	})
	if err != nil {
		t.Fatalf("marshal replay fixture failed: %v", err)
	}

	if err := fixtureStore.ReplayFixture().Upsert(&store.ReplayFixture{
		FixtureID:   fixtureID,
		FixtureType: "user_stream",
		Version:     "v1",
		Source:      "synthetic_phase7",
		PayloadJSON: string(payload),
	}); err != nil {
		t.Fatalf("upsert replay fixture failed: %v", err)
	}
}

func seedPhase7MigrationFixture(t *testing.T, fixtureStore *store.Store, fixtureID, userID, traderID string) {
	t.Helper()
	if fixtureStore == nil {
		t.Fatalf("validation fixture store is required")
	}

	payload, err := json.Marshal(schemaMigrationFixturePayload{
		SchemaSQL: []string{
			`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL, password_hash TEXT NOT NULL, created_at TIMESTAMP, updated_at TIMESTAMP);`,
			`CREATE UNIQUE INDEX idx_users_email ON users(email);`,
			`CREATE TABLE ai_models (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, name TEXT NOT NULL, provider TEXT NOT NULL, enabled BOOLEAN DEFAULT FALSE, api_key TEXT DEFAULT '', custom_api_url TEXT DEFAULT '', custom_model_name TEXT DEFAULT '', created_at TIMESTAMP, updated_at TIMESTAMP);`,
			`CREATE TABLE exchanges (id TEXT PRIMARY KEY, exchange_type TEXT NOT NULL DEFAULT '', account_name TEXT NOT NULL DEFAULT '', user_id TEXT NOT NULL, name TEXT NOT NULL, type TEXT NOT NULL, enabled BOOLEAN DEFAULT FALSE, api_key TEXT DEFAULT '', secret_key TEXT DEFAULT '', passphrase TEXT DEFAULT '', testnet BOOLEAN DEFAULT FALSE, hyperliquid_wallet_addr TEXT DEFAULT '', hyperliquid_unified_account BOOLEAN DEFAULT TRUE, aster_user TEXT DEFAULT '', aster_signer TEXT DEFAULT '', aster_private_key TEXT DEFAULT '', lighter_wallet_addr TEXT DEFAULT '', lighter_private_key TEXT DEFAULT '', lighter_api_key_private_key TEXT DEFAULT '', lighter_api_key_index INTEGER DEFAULT 0, created_at TIMESTAMP, updated_at TIMESTAMP);`,
			`CREATE TABLE traders (id TEXT PRIMARY KEY, user_id TEXT NOT NULL DEFAULT 'default', name TEXT NOT NULL, ai_model_id TEXT NOT NULL, exchange_id TEXT NOT NULL, strategy_id TEXT DEFAULT '', initial_balance REAL NOT NULL, scan_interval_minutes INTEGER DEFAULT 3, is_running BOOLEAN DEFAULT FALSE, is_cross_margin BOOLEAN DEFAULT TRUE, created_at TIMESTAMP, updated_at TIMESTAMP);`,
		},
		SeedSQL: []string{
			fmt.Sprintf(`INSERT INTO users (id, email, password_hash, created_at, updated_at) VALUES ('%s', '%s@example.com', 'phase7-migration', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`, userID, userID),
			fmt.Sprintf(`INSERT INTO ai_models (id, user_id, name, provider, enabled, api_key, custom_api_url, created_at, updated_at) VALUES ('%s_minimax', '%s', 'MiniMax', 'minimax', 1, 'test-key', 'https://example.com/anthropic', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`, userID, userID),
			fmt.Sprintf(`INSERT INTO exchanges (id, exchange_type, account_name, user_id, name, type, enabled, api_key, secret_key, created_at, updated_at) VALUES ('%s-exchange', 'binance', 'Default', '%s', 'Binance Futures', 'cex', 1, 'api-key', 'secret-key', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`, traderID, userID),
			fmt.Sprintf(`INSERT INTO traders (id, user_id, name, ai_model_id, exchange_id, initial_balance, scan_interval_minutes, is_running, is_cross_margin, created_at, updated_at) VALUES ('%s', '%s', 'Phase7 Legacy Trader', '%s_minimax', '%s-exchange', 1000, 3, 0, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`, traderID, userID, userID, traderID),
		},
		ValidationTraderID:  traderID,
		ValidationUserID:    userID,
		SelectedSymbol:      "BTCUSDT",
		ExecutionMode:       "readonly",
		UserStreamReady:     false,
		AllowOrderPlacement: false,
	})
	if err != nil {
		t.Fatalf("marshal schema migration fixture failed: %v", err)
	}

	if err := fixtureStore.SchemaMigrationFixture().Upsert(&store.SchemaMigrationFixture{
		FixtureID:   fixtureID,
		Phase:       "phase1",
		Version:     "v1",
		Source:      "synthetic_phase7",
		PayloadJSON: string(payload),
	}); err != nil {
		t.Fatalf("upsert schema migration fixture failed: %v", err)
	}
}

func hasPhase7CapabilityReason(reasons []CapabilityReason, action, category, contains string) bool {
	for _, reason := range reasons {
		if reason.Action != action || reason.Category != category {
			continue
		}
		if contains == "" || strings.Contains(strings.ToLower(reason.Reason), strings.ToLower(contains)) {
			return true
		}
	}
	return false
}

func TestBinanceUserStreamReplayManager_ReplaysRecordedEvents(t *testing.T) {
	st := newPhase3TraderStore(t)
	fixtureStore := newPhase7ValidationFixtureStore(t)
	userID := "phase7-replay-user"
	traderID := "phase7-replay-trader"
	symbol := "BTCUSDT"
	side := "LONG"

	seedPhase7RuntimeIdentity(t, st, userID, traderID)
	plan, levels := seedPhase7ScaleOutProtectedScope(t, st, traderID, symbol, side)
	if len(levels) != 2 {
		t.Fatalf("expected 2 scale-out levels, got %d", len(levels))
	}

	openOrders := buildPhase7WorkingOpenOrders(t, st, traderID, symbol)
	if len(openOrders) != 3 {
		t.Fatalf("expected 3 working open orders in replay snapshot, got %d", len(openOrders))
	}

	fixtureID := "phase7-replay-fixture"
	seedPhase7ReplayFixture(t, fixtureStore, fixtureID, traderID, symbol, openOrders, []replayFixtureEventPayload{
		{
			EventType:   "ORDER_TRADE_UPDATE",
			EventSource: "recorded_binance_user_stream",
			Label:       "repeat filled reduce level for idempotent replay",
			EventTime:   time.Now().UTC(),
			Payload: mustPhase7ReplayPayload(t, map[string]interface{}{
				"e": "ORDER_TRADE_UPDATE",
				"E": time.Now().UTC().UnixMilli(),
				"T": time.Now().UTC().UnixMilli(),
				"o": map[string]interface{}{
					"s":  symbol,
					"c":  levels[0].LinkedOrderIntentID,
					"S":  "SELL",
					"o":  "LIMIT",
					"f":  "GTC",
					"q":  "0.2",
					"p":  "67500",
					"ap": "67500",
					"sp": "0",
					"z":  "0.2",
					"X":  "FILLED",
					"i":  7101,
					"R":  true,
					"cp": false,
					"ps": side,
				},
			}),
		},
		{
			EventType:   "ORDER_TRADE_UPDATE",
			EventSource: "recorded_binance_user_stream",
			Label:       "arm cancel_replace on fixed protection stop",
			EventTime:   time.Now().UTC().Add(1 * time.Second),
			Payload: mustPhase7ReplayPayload(t, map[string]interface{}{
				"e": "ORDER_TRADE_UPDATE",
				"E": time.Now().UTC().Add(1 * time.Second).UnixMilli(),
				"T": time.Now().UTC().Add(1 * time.Second).UnixMilli(),
				"o": map[string]interface{}{
					"s":  symbol,
					"c":  "pg-phase7-" + strings.ToLower(symbol) + "-sl-replace",
					"S":  "SELL",
					"o":  "STOP_MARKET",
					"f":  "GTC",
					"q":  "0.8",
					"p":  "64100",
					"ap": "0",
					"sp": "64100",
					"z":  "0",
					"X":  "PENDING_REPLACE",
					"i":  7103,
					"R":  true,
					"cp": true,
					"ps": side,
				},
			}),
		},
		{
			EventType:   "ORDER_TRADE_UPDATE",
			EventSource: "recorded_binance_user_stream",
			Label:       "scale-in entry order acknowledged while replay stays idempotent",
			EventTime:   time.Now().UTC().Add(2 * time.Second),
			Payload: mustPhase7ReplayPayload(t, map[string]interface{}{
				"e": "ORDER_TRADE_UPDATE",
				"E": time.Now().UTC().Add(2 * time.Second).UnixMilli(),
				"T": time.Now().UTC().Add(2 * time.Second).UnixMilli(),
				"o": map[string]interface{}{
					"s":  symbol,
					"c":  "si-phase7-" + strings.ToLower(symbol) + "-l1",
					"S":  "BUY",
					"o":  "MARKET",
					"f":  "GTC",
					"q":  "0.1",
					"p":  "0",
					"ap": "65050",
					"sp": "0",
					"z":  "0.1",
					"X":  "FILLED",
					"i":  8101,
					"R":  false,
					"cp": false,
					"ps": side,
				},
			}),
		},
	})

	manager := NewBinanceUserStreamReplayManager(st)
	preview, err := manager.RunFixture(fixtureID, "duplicate")
	if err != nil {
		t.Fatalf("run replay fixture failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected replay preview")
	}
	if preview.FixtureID != fixtureID {
		t.Fatalf("expected fixture id %s, got %s", fixtureID, preview.FixtureID)
	}
	if preview.EventCount != 6 {
		t.Fatalf("expected duplicate replay to expand to 6 events, got %d", preview.EventCount)
	}
	if preview.TruthSnapshot == nil || preview.TruthSnapshot.PositionAggregate == nil {
		t.Fatalf("expected truth snapshot aggregate after replay")
	}
	if preview.TruthSnapshot.PositionAggregate.Symbol != symbol {
		t.Fatalf("expected replay aggregate symbol %s, got %s", symbol, preview.TruthSnapshot.PositionAggregate.Symbol)
	}
	if preview.TruthSnapshot.PositionAggregate.ScaleOutStatus != "partially_filled" {
		t.Fatalf("expected partially_filled scale-out status after replay, got %s", preview.TruthSnapshot.PositionAggregate.ScaleOutStatus)
	}
	if preview.TruthSnapshot.PositionAggregate.ProtectionGroupID == "" {
		t.Fatalf("expected protection group id in replay snapshot")
	}
	if preview.CapabilityPreview == nil {
		t.Fatalf("expected capability preview after replay")
	}
	if preview.CapabilityPreview.TruthSnapshotVersion == "" {
		t.Fatalf("expected truth snapshot version metadata in capability preview")
	}
	if preview.ReplayedFromFixture != fixtureID {
		t.Fatalf("expected replay metadata fixture id %s, got %s", fixtureID, preview.ReplayedFromFixture)
	}

	updatedPlan, err := st.ScaleOutPlan().GetByKeys(traderID, plan.ScaleOutPlanID, plan.LinkedPositionKey)
	if err != nil {
		t.Fatalf("reload scale-out plan failed: %v", err)
	}
	if updatedPlan == nil || updatedPlan.Status != "partially_filled" {
		t.Fatalf("expected idempotent replay to keep partially_filled plan, got %#v", updatedPlan)
	}
}

func TestTruthLayerRestoreManager_RestoresConsistentSnapshot(t *testing.T) {
	st := newPhase3TraderStore(t)
	userID := "phase7-restore-user"
	traderID := "phase7-restore-trader"
	symbol := "BTCUSDT"

	seedPhase7RuntimeIdentity(t, st, userID, traderID)
	seedPhase7ScaleOutProtectedScope(t, st, traderID, symbol, "LONG")

	manager := NewTruthLayerRestoreManager(st)
	preview, err := manager.RestoreTraderTruth(userID, traderID, symbol, "readonly", true, false)
	if err != nil {
		t.Fatalf("restore trader truth failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected restore preview")
	}
	if !preview.RestoredFromSnapshot {
		t.Fatalf("expected restore metadata to mark restored snapshot")
	}
	if preview.HasMismatch {
		t.Fatalf("did not expect restore snapshot mismatch, got %#v", preview.MismatchReasons)
	}
	if preview.BeforeSnapshot == nil || preview.AfterSnapshot == nil {
		t.Fatalf("expected before/after snapshots in restore preview")
	}
	if preview.BeforeSnapshot.PositionAggregate == nil || preview.AfterSnapshot.PositionAggregate == nil {
		t.Fatalf("expected position aggregate in restore snapshots")
	}
	if preview.BeforeSnapshot.PositionAggregate.TotalQty != preview.AfterSnapshot.PositionAggregate.TotalQty {
		t.Fatalf("expected restore to preserve total quantity, got before=%.6f after=%.6f",
			preview.BeforeSnapshot.PositionAggregate.TotalQty, preview.AfterSnapshot.PositionAggregate.TotalQty)
	}
	if preview.CapabilityPreview == nil {
		t.Fatalf("expected capability preview during restore validation")
	}
}

func TestConflictMatrixRunner_ExecutesLongChainConflictCases(t *testing.T) {
	st := newPhase3TraderStore(t)
	userID := "phase7-conflict-user"
	traderID := "phase7-conflict-trader"
	symbol := "BTCUSDT"
	side := "LONG"

	seedPhase7RuntimeIdentity(t, st, userID, traderID)
	seedPhase7ScaleOutProtectedScope(t, st, traderID, symbol, side)

	runner := NewConflictMatrixRunner(st)
	result, err := runner.RunCase(&ConflictMatrixCase{
		CaseID:              "scale-out-plus-scale-in-conflict",
		TraderID:            traderID,
		UserID:              userID,
		SelectedSymbol:      symbol,
		ExecutionMode:       "live",
		UserStreamReady:     true,
		AllowOrderPlacement: true,
		EventSequence: []string{
			"active scale-out plan remains armed",
			"conflicting scale-in plan is recovered from truth layer",
			"runtime capabilities are re-evaluated against the conflict state",
		},
		Run: func(st *store.Store) error {
			_, err := st.ScaleInPlanBuilder().ApplyScaleInEvent(&store.ScaleInPlanEventInput{
				TraderID:            traderID,
				Symbol:              symbol,
				Side:                side,
				LinkedPositionKey:   symbol + "|" + side,
				ScaleInPlanID:       "si-phase7-conflict",
				PlanMode:            "fixed_qty",
				Status:              "armed",
				TotalPlannedQty:     0.1,
				RemainingPlannedQty: 0.1,
				ExecutedQty:         0,
				MaxScaleInCount:     2,
				CurrentScaleInCount: 0,
				Source:              "conflict_matrix",
				EventType:           "SCALE_IN_PLAN_CREATED",
				EventSource:         "conflict_matrix",
				PayloadJSON:         `{"event":"scale_in_plan_created"}`,
				EventTime:           time.Now().UTC(),
				Levels: []*store.ScaleInPlanLevelInput{
					{
						TraderID:              traderID,
						Symbol:                symbol,
						Side:                  side,
						LinkedPositionKey:     symbol + "|" + side,
						ScaleInPlanID:         "si-phase7-conflict",
						LevelIndex:            1,
						TargetType:            "market",
						TargetPrice:           65000,
						PlannedQty:            0.1,
						ExecutedQty:           0,
						RemainingQty:          0.1,
						LinkedOrderIntentID:   "si-phase7-conflict-l1",
						LinkedExchangeOrderID: "9101",
						Status:                "armed",
					},
				},
			})
			if err != nil {
				return err
			}
			_, err = st.OrderRegistryBuilder().ApplyOrderEvent(&store.OrderRegistryEventInput{
				TraderID:          traderID,
				Symbol:            symbol,
				Side:              "BUY",
				PositionSideMode:  "one_way",
				OrderRole:         "entry",
				LocalIntentID:     "si-phase7-conflict-l1",
				LinkedGroupID:     "si-phase7-conflict",
				LinkedPositionKey: symbol + "|" + side,
				ExchangeOrderID:   "9101",
				ClientOrderID:     "si-phase7-conflict-l1-client",
				OrderType:         "MARKET",
				TimeInForce:       "GTC",
				ReduceOnly:        false,
				ClosePosition:     false,
				OrigQty:           0.1,
				ExecutedQty:       0,
				Status:            "NEW",
				Source:            "conflict_matrix",
				EventType:         "ORDER_NEW",
				EventSource:       "conflict_matrix",
				PayloadJSON:       `{"event":"scale_in_level_armed"}`,
				EventTime:         time.Now().UTC(),
			})
			return err
		},
	})
	if err != nil {
		t.Fatalf("run conflict matrix case failed: %v", err)
	}
	if result == nil {
		t.Fatalf("expected conflict matrix result")
	}
	if result.CaseID != "scale-out-plus-scale-in-conflict" {
		t.Fatalf("unexpected conflict case id %s", result.CaseID)
	}
	if result.FinalSnapshot == nil || result.FinalSnapshot.ScaleInSummary == nil {
		t.Fatalf("expected final snapshot to include scale-in summary")
	}
	if !result.FinalSnapshot.ScaleInSummary.HasScaleInPlan {
		t.Fatalf("expected conflict case to produce active scale-in plan")
	}
	if result.FinalSnapshot.ScaleOutSummary == nil || !result.FinalSnapshot.ScaleOutSummary.HasScaleOutPlan {
		t.Fatalf("expected conflict case to keep the active scale-out plan in the final snapshot")
	}
	if result.CapabilityPreview == nil {
		t.Fatalf("expected capability preview for conflict case")
	}
	if !hasPhase7CapabilityReason(result.CapabilityPreview.ScaleInBlockReasons, "add_position", "scale_in", "active scale-in plan") &&
		!hasPhase7CapabilityReason(result.CapabilityPreview.ScaleInBlockReasons, "add_position", "order_state", "reserved pending entry quantity") {
		t.Fatalf("expected add_position to be blocked by the conflicting truth state, got %#v", result.CapabilityPreview.ScaleInBlockReasons)
	}
	if !result.HasMismatch {
		t.Fatalf("expected conflict case to surface mismatch evidence because working orders lack exchange snapshot")
	}
}

func TestSchemaMigrationRegressionRunner_UpgradesLegacySQLiteFixture(t *testing.T) {
	st := newPhase3TraderStore(t)
	fixtureStore := newPhase7ValidationFixtureStore(t)
	userID := "phase7-migration-user"
	traderID := "phase7-migration-trader"
	fixtureID := "phase7-schema-fixture"

	seedPhase7MigrationFixture(t, fixtureStore, fixtureID, userID, traderID)

	runner := NewSchemaMigrationRegressionRunner()
	preview, err := runner.RunFixture(st, fixtureID)
	if err != nil {
		t.Fatalf("run schema migration regression failed: %v", err)
	}
	if preview == nil {
		t.Fatalf("expected migration regression preview")
	}
	if preview.FixtureID != fixtureID {
		t.Fatalf("expected fixture id %s, got %s", fixtureID, preview.FixtureID)
	}
	if preview.MigrationFixtureVersion != "v1" {
		t.Fatalf("expected migration fixture version v1, got %s", preview.MigrationFixtureVersion)
	}
	if preview.HasMismatch {
		t.Fatalf("did not expect migration regression mismatch, got %#v", preview.MismatchReasons)
	}
	if preview.CapabilityPreview == nil {
		t.Fatalf("expected capability preview after migration regression")
	}
	if preview.CapabilityPreview.TruthSnapshotVersion == "" {
		t.Fatalf("expected truth snapshot metadata on migrated capability preview")
	}
}
