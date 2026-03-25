//go:build ignore
// +build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"nofx/store"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	targetTraderID         = "GLOBAL_REAL_SNIPER"
	orderMatchWindow       = 5 * time.Minute
	positionMatchWindow    = 5 * time.Minute
	defaultDatabasePath    = "data/data.db"
	realBacktestShadowStep = 3 * time.Minute
	shadowLeadInCycles     = 2
)

type decisionRecordRow struct {
	ID        int64     `gorm:"column:id"`
	TraderID  string    `gorm:"column:trader_id"`
	Timestamp time.Time `gorm:"column:timestamp"`
	Decisions string    `gorm:"column:decisions"`
}

type decisionAction struct {
	Action        string    `json:"action"`
	Symbol        string    `json:"symbol"`
	Timestamp     time.Time `json:"timestamp"`
	Reasoning     string    `json:"reasoning"`
	ExecutionMode string    `json:"execution_mode"`
}

type actionEvent struct {
	LegacyTraderID string
	Action         string
	Symbol         string
	TimestampMs    int64
}

type migrationSummary struct {
	LegacyTraderIDs []string
	DecisionRecords int64
	ShadowSnapshots int64
	TraderOrders    int64
	TraderFills     int64
	TraderPositions int64
}

func main() {
	dbPath := flag.String("db", defaultDatabasePath, "database path")
	flag.Parse()

	gdb, err := store.InitGorm(*dbPath)
	if err != nil {
		log.Fatalf("open database %s: %v", *dbPath, err)
	}

	summary, err := migrate(gdb)
	if err != nil {
		log.Fatalf("migrate legacy real-backtest records: %v", err)
	}

	fmt.Printf("Migrated legacy real-backtest records in %s\n", filepath.Clean(*dbPath))
	fmt.Printf("Legacy trader IDs: %s\n", strings.Join(summary.LegacyTraderIDs, ", "))
	fmt.Printf("decision_records=%d shadow_snapshots=%d trader_orders=%d trader_fills=%d trader_positions=%d\n",
		summary.DecisionRecords,
		summary.ShadowSnapshots,
		summary.TraderOrders,
		summary.TraderFills,
		summary.TraderPositions,
	)
}

func migrate(gdb *gorm.DB) (*migrationSummary, error) {
	records, err := loadLegacyRealBacktestDecisionRecords(gdb)
	if err != nil {
		return nil, err
	}

	decisionIDs := make([]int64, 0, len(records))
	actionEvents := make([]actionEvent, 0, len(records)*2)
	legacyTraders := make(map[string]struct{})
	shadowWindows := make(map[string][2]int64)

	for _, record := range records {
		decisionIDs = append(decisionIDs, record.ID)
		legacyTraders[record.TraderID] = struct{}{}

		tickMs := record.Timestamp.UTC().UnixMilli()
		minMax := shadowWindows[record.TraderID]
		if minMax[0] == 0 || tickMs < minMax[0] {
			minMax[0] = tickMs
		}
		if tickMs > minMax[1] {
			minMax[1] = tickMs
		}
		shadowWindows[record.TraderID] = minMax

		actions, parseErr := parseRealBacktestActions(record)
		if parseErr != nil {
			return nil, fmt.Errorf("parse decision record %d: %w", record.ID, parseErr)
		}
		for _, action := range actions {
			eventTime := action.Timestamp.UTC()
			if eventTime.IsZero() {
				eventTime = record.Timestamp.UTC()
			}
			actionEvents = append(actionEvents, actionEvent{
				LegacyTraderID: record.TraderID,
				Action:         action.Action,
				Symbol:         normalizeSymbol(action.Symbol),
				TimestampMs:    eventTime.UnixMilli(),
			})
		}
	}

	legacyTraderIDs := mapsKeys(legacyTraders)
	slices.Sort(legacyTraderIDs)

	summary := &migrationSummary{LegacyTraderIDs: legacyTraderIDs}
	if len(legacyTraderIDs) == 0 {
		return summary, nil
	}

	err = gdb.Transaction(func(tx *gorm.DB) error {
		if len(decisionIDs) > 0 {
			result := tx.Table("decision_records").
				Where("id IN ?", decisionIDs).
				Update("trader_id", targetTraderID)
			if result.Error != nil {
				return fmt.Errorf("update decision_records: %w", result.Error)
			}
			summary.DecisionRecords = result.RowsAffected
		}

		shadowCount, shadowErr := migrateShadowSnapshots(tx, shadowWindows)
		if shadowErr != nil {
			return shadowErr
		}
		summary.ShadowSnapshots = shadowCount

		orderIDs, fillIDs, positionIDs, collectErr := collectExecutionRecordIDs(tx, actionEvents)
		if collectErr != nil {
			return collectErr
		}

		if len(orderIDs) > 0 {
			result := tx.Table("trader_orders").Where("id IN ?", orderIDs).Update("trader_id", targetTraderID)
			if result.Error != nil {
				return fmt.Errorf("update trader_orders: %w", result.Error)
			}
			summary.TraderOrders = result.RowsAffected
		}
		if len(fillIDs) > 0 {
			result := tx.Table("trader_fills").Where("id IN ?", fillIDs).Update("trader_id", targetTraderID)
			if result.Error != nil {
				return fmt.Errorf("update trader_fills: %w", result.Error)
			}
			summary.TraderFills = result.RowsAffected
		}
		if len(positionIDs) > 0 {
			result := tx.Table("trader_positions").Where("id IN ?", positionIDs).Update("trader_id", targetTraderID)
			if result.Error != nil {
				return fmt.Errorf("update trader_positions: %w", result.Error)
			}
			summary.TraderPositions = result.RowsAffected
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return summary, nil
}

func loadLegacyRealBacktestDecisionRecords(gdb *gorm.DB) ([]decisionRecordRow, error) {
	var rows []decisionRecordRow
	err := gdb.Table("decision_records").
		Select("id, trader_id, timestamp, decisions").
		Where("trader_id <> ?", targetTraderID).
		Where("decisions LIKE ? OR decisions LIKE ?", "%real_backtest%", "%实战回测%").
		Order("timestamp ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("query decision_records: %w", err)
	}

	filtered := make([]decisionRecordRow, 0, len(rows))
	for _, row := range rows {
		actions, parseErr := parseRealBacktestActions(row)
		if parseErr != nil {
			return nil, parseErr
		}
		if len(actions) == 0 {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered, nil
}

func parseRealBacktestActions(row decisionRecordRow) ([]decisionAction, error) {
	var actions []decisionAction
	if err := json.Unmarshal([]byte(row.Decisions), &actions); err != nil {
		return nil, fmt.Errorf("unmarshal decisions JSON: %w", err)
	}

	filtered := make([]decisionAction, 0, len(actions))
	for _, action := range actions {
		if isRealBacktestAction(action.ExecutionMode, action.Reasoning) {
			filtered = append(filtered, action)
		}
	}
	return filtered, nil
}

func isRealBacktestAction(executionMode, reasoning string) bool {
	if strings.EqualFold(strings.TrimSpace(executionMode), "real_backtest") {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(reasoning))
	return strings.Contains(reason, "real backtest") || strings.Contains(reasoning, "实战回测")
}

func migrateShadowSnapshots(tx *gorm.DB, shadowWindows map[string][2]int64) (int64, error) {
	var migrated int64

	for legacyTraderID, window := range shadowWindows {
		if window[0] == 0 || window[1] == 0 {
			continue
		}

		leadInMs := int64(shadowLeadInCycles) * realBacktestShadowStep.Milliseconds()
		startMs := floorToInterval(window[0]-leadInMs, realBacktestShadowStep)
		endMs := ceilToInterval(window[1], realBacktestShadowStep)

		var rows []store.ShadowSnapshot
		if err := tx.Where(
			"trader_id = ? AND decision_time BETWEEN ? AND ?",
			legacyTraderID,
			startMs,
			endMs,
		).Order("decision_time ASC, symbol ASC").Find(&rows).Error; err != nil {
			return migrated, fmt.Errorf("load shadow_snapshots for %s: %w", legacyTraderID, err)
		}
		if len(rows) == 0 {
			continue
		}

		targetRows := make([]store.ShadowSnapshot, 0, len(rows))
		ids := make([]uint, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			row.ID = 0
			row.TraderID = targetTraderID
			targetRows = append(targetRows, row)
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "trader_id"},
				{Name: "decision_time"},
				{Name: "symbol"},
			},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"sector":               gorm.Expr("excluded.sector"),
				"action_taken":         gorm.Expr("excluded.action_taken"),
				"price_t0":             gorm.Expr("excluded.price_t0"),
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
				"filled":               gorm.Expr("excluded.filled"),
				"price_t1":             gorm.Expr("excluded.price_t1"),
				"return_pct":           gorm.Expr("excluded.return_pct"),
				"filled_at":            gorm.Expr("excluded.filled_at"),
				"created_at":           gorm.Expr("excluded.created_at"),
				"updated_at":           gorm.Expr("excluded.updated_at"),
			}),
		}).Create(&targetRows).Error; err != nil {
			return migrated, fmt.Errorf("upsert GLOBAL_REAL_SNIPER shadow snapshots: %w", err)
		}

		deleteResult := tx.Where("id IN ?", ids).Delete(&store.ShadowSnapshot{})
		if deleteResult.Error != nil {
			return migrated, fmt.Errorf("delete legacy shadow_snapshots: %w", deleteResult.Error)
		}
		migrated += deleteResult.RowsAffected
	}

	return migrated, nil
}

func collectExecutionRecordIDs(tx *gorm.DB, actions []actionEvent) ([]int64, []int64, []int64, error) {
	orderIDs := make(map[int64]struct{})
	fillIDs := make(map[int64]struct{})
	positionIDs := make(map[int64]struct{})

	for _, action := range actions {
		if action.Symbol == "" || action.TimestampMs <= 0 {
			continue
		}

		startMs := action.TimestampMs - orderMatchWindow.Milliseconds()
		endMs := action.TimestampMs + orderMatchWindow.Milliseconds()

		var ids []int64
		if err := tx.Table("trader_orders").
			Select("id").
			Where("trader_id = ? AND symbol = ? AND order_action = ? AND created_at BETWEEN ? AND ?",
				action.LegacyTraderID, action.Symbol, action.Action, startMs, endMs).
			Find(&ids).Error; err != nil {
			return nil, nil, nil, fmt.Errorf("query trader_orders ids: %w", err)
		}
		for _, id := range ids {
			orderIDs[id] = struct{}{}
		}

		var fillSide string
		switch action.Action {
		case "open_long", "close_short":
			fillSide = "BUY"
		case "open_short", "close_long":
			fillSide = "SELL"
		}
		if fillSide != "" {
			ids = ids[:0]
			if err := tx.Table("trader_fills").
				Select("id").
				Where("trader_id = ? AND symbol = ? AND side = ? AND created_at BETWEEN ? AND ?",
					action.LegacyTraderID, action.Symbol, fillSide, startMs, endMs).
				Find(&ids).Error; err != nil {
				return nil, nil, nil, fmt.Errorf("query trader_fills ids: %w", err)
			}
			for _, id := range ids {
				fillIDs[id] = struct{}{}
			}
		}

		positionStartMs := action.TimestampMs - positionMatchWindow.Milliseconds()
		positionEndMs := action.TimestampMs + positionMatchWindow.Milliseconds()
		positionSide := "LONG"
		if strings.HasSuffix(action.Action, "_short") {
			positionSide = "SHORT"
		}
		timeColumn := "entry_time"
		if strings.HasPrefix(action.Action, "close_") {
			timeColumn = "exit_time"
		}

		ids = ids[:0]
		if err := tx.Table("trader_positions").
			Select("id").
			Where(
				fmt.Sprintf("trader_id = ? AND symbol = ? AND side = ? AND %s BETWEEN ? AND ?", timeColumn),
				action.LegacyTraderID,
				action.Symbol,
				positionSide,
				positionStartMs,
				positionEndMs,
			).Find(&ids).Error; err != nil {
			return nil, nil, nil, fmt.Errorf("query trader_positions ids: %w", err)
		}
		for _, id := range ids {
			positionIDs[id] = struct{}{}
		}
	}

	return mapsKeysInt64(orderIDs), mapsKeysInt64(fillIDs), mapsKeysInt64(positionIDs), nil
}

func normalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

func floorToInterval(value int64, interval time.Duration) int64 {
	size := interval.Milliseconds()
	if size <= 0 {
		return value
	}
	return (value / size) * size
}

func ceilToInterval(value int64, interval time.Duration) int64 {
	size := interval.Milliseconds()
	if size <= 0 {
		return value
	}
	if value%size == 0 {
		return value
	}
	return ((value / size) + 1) * size
}

func mapsKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func mapsKeysInt64(values map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
