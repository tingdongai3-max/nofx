//go:build ignore
// +build ignore

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"nofx/market"
	"nofx/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type migrationStats struct {
	TotalRows           int64
	LegacyRows          int64
	ConsensusRows       int64
	FilledLegacyRows    int64
	FilledConsensusRows int64
	LegacyUniqueKeys    int64
	ConsensusUniqueKeys int64
}

type snapshotKey struct {
	DecisionTime int64
	Symbol       string
}

func main() {
	dbType := flag.String("db-type", defaultEnv("DB_TYPE", "sqlite"), "database type: sqlite or postgres")
	dbPath := flag.String("db-path", defaultEnv("DB_PATH", "data/data.db"), "sqlite database path")
	dbHost := flag.String("db-host", defaultEnv("DB_HOST", "localhost"), "postgres host")
	dbPort := flag.Int("db-port", defaultEnvInt("DB_PORT", 5432), "postgres port")
	dbUser := flag.String("db-user", defaultEnv("DB_USER", "postgres"), "postgres user")
	dbPassword := flag.String("db-password", os.Getenv("DB_PASSWORD"), "postgres password")
	dbName := flag.String("db-name", defaultEnv("DB_NAME", "nofx"), "postgres database name")
	dbSSLMode := flag.String("db-sslmode", defaultEnv("DB_SSLMODE", "disable"), "postgres sslmode")
	dryRun := flag.Bool("dry-run", false, "print planned counts without writing")
	flag.Parse()

	cfg := store.DBConfig{
		Type:     store.DBType(strings.ToLower(strings.TrimSpace(*dbType))),
		Path:     strings.TrimSpace(*dbPath),
		Host:     strings.TrimSpace(*dbHost),
		Port:     *dbPort,
		User:     strings.TrimSpace(*dbUser),
		Password: *dbPassword,
		DBName:   strings.TrimSpace(*dbName),
		SSLMode:  strings.TrimSpace(*dbSSLMode),
	}

	db, err := store.InitGormWithConfig(cfg)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("get sql.DB: %v", err)
	}
	defer sqlDB.Close()

	before, err := collectStats(db)
	if err != nil {
		log.Fatalf("collect pre-migration stats: %v", err)
	}
	printStats("before", before)

	mergedRows, err := buildConsensusRows(db)
	if err != nil {
		log.Fatalf("build consensus rows: %v", err)
	}

	log.Printf("planned consensus upserts: %d", len(mergedRows))
	log.Printf("legacy duplicate collisions by (decision_time,symbol): %d", before.LegacyRows-before.LegacyUniqueKeys)
	if *dryRun {
		log.Printf("dry-run enabled; no data written")
		if err := verifyAdaptiveState(cfg); err != nil {
			log.Fatalf("verify adaptive state: %v", err)
		}
		return
	}

	if err := upsertConsensusRows(db, mergedRows); err != nil {
		log.Fatalf("upsert consensus rows: %v", err)
	}

	after, err := collectStats(db)
	if err != nil {
		log.Fatalf("collect post-migration stats: %v", err)
	}
	printStats("after", after)

	if after.ConsensusRows < before.LegacyUniqueKeys {
		log.Fatalf("consensus rows incomplete: expected at least %d unique keys, got %d", before.LegacyUniqueKeys, after.ConsensusRows)
	}

	if err := verifyAdaptiveState(cfg); err != nil {
		log.Fatalf("verify adaptive state: %v", err)
	}

	log.Printf("migration completed successfully")
}

func collectStats(db *gorm.DB) (migrationStats, error) {
	var stats migrationStats

	if err := db.Model(&store.ShadowSnapshot{}).Count(&stats.TotalRows).Error; err != nil {
		return stats, err
	}
	if err := db.Model(&store.ShadowSnapshot{}).
		Where("trader_id <> ?", store.GlobalConsensusTraderID).
		Count(&stats.LegacyRows).Error; err != nil {
		return stats, err
	}
	if err := db.Model(&store.ShadowSnapshot{}).
		Where("trader_id = ?", store.GlobalConsensusTraderID).
		Count(&stats.ConsensusRows).Error; err != nil {
		return stats, err
	}
	if err := db.Model(&store.ShadowSnapshot{}).
		Where("trader_id <> ? AND filled = ?", store.GlobalConsensusTraderID, true).
		Count(&stats.FilledLegacyRows).Error; err != nil {
		return stats, err
	}
	if err := db.Model(&store.ShadowSnapshot{}).
		Where("trader_id = ? AND filled = ?", store.GlobalConsensusTraderID, true).
		Count(&stats.FilledConsensusRows).Error; err != nil {
		return stats, err
	}
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT decision_time, symbol
			FROM shadow_snapshots
			WHERE trader_id <> ?
			GROUP BY decision_time, symbol
		) AS legacy_keys
	`, store.GlobalConsensusTraderID).Scan(&stats.LegacyUniqueKeys).Error; err != nil {
		return stats, err
	}
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT decision_time, symbol
			FROM shadow_snapshots
			WHERE trader_id = ?
			GROUP BY decision_time, symbol
		) AS consensus_keys
	`, store.GlobalConsensusTraderID).Scan(&stats.ConsensusUniqueKeys).Error; err != nil {
		return stats, err
	}

	return stats, nil
}

func printStats(label string, stats migrationStats) {
	log.Printf(
		"%s stats: total=%d legacy=%d consensus=%d filled_legacy=%d filled_consensus=%d legacy_unique=%d consensus_unique=%d",
		label,
		stats.TotalRows,
		stats.LegacyRows,
		stats.ConsensusRows,
		stats.FilledLegacyRows,
		stats.FilledConsensusRows,
		stats.LegacyUniqueKeys,
		stats.ConsensusUniqueKeys,
	)
}

func buildConsensusRows(db *gorm.DB) ([]store.ShadowSnapshot, error) {
	var legacyRows []store.ShadowSnapshot
	if err := db.
		Where("trader_id <> ?", store.GlobalConsensusTraderID).
		Order("decision_time ASC, updated_at ASC, id ASC").
		Find(&legacyRows).Error; err != nil {
		return nil, err
	}

	merged := make(map[snapshotKey]store.ShadowSnapshot, len(legacyRows))
	for _, row := range legacyRows {
		key := snapshotKey{
			DecisionTime: row.DecisionTime,
			Symbol:       strings.TrimSpace(row.Symbol),
		}
		if key.Symbol == "" {
			continue
		}

		clone := row
		clone.ID = 0
		clone.TraderID = store.GlobalConsensusTraderID

		current, exists := merged[key]
		if !exists {
			merged[key] = clone
			continue
		}

		filledSource := chooseFilledSource(current, clone)
		clone.ActionTaken = maxInt(current.ActionTaken, clone.ActionTaken)
		clone.CreatedAt = minPositive(current.CreatedAt, clone.CreatedAt)
		if filledSource != nil {
			clone.Filled = true
			clone.PriceT1 = filledSource.PriceT1
			clone.ReturnPct = filledSource.ReturnPct
			clone.FilledAt = filledSource.FilledAt
		}
		merged[key] = clone
	}

	rows := make([]store.ShadowSnapshot, 0, len(merged))
	for _, row := range merged {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].DecisionTime == rows[j].DecisionTime {
			return rows[i].Symbol < rows[j].Symbol
		}
		return rows[i].DecisionTime < rows[j].DecisionTime
	})
	return rows, nil
}

func chooseFilledSource(a, b store.ShadowSnapshot) *store.ShadowSnapshot {
	aFilled := a.Filled && a.FilledAt > 0
	bFilled := b.Filled && b.FilledAt > 0

	switch {
	case aFilled && !bFilled:
		return &a
	case !aFilled && bFilled:
		return &b
	case !aFilled && !bFilled:
		return nil
	case b.FilledAt > a.FilledAt:
		return &b
	case b.FilledAt < a.FilledAt:
		return &a
	case b.UpdatedAt >= a.UpdatedAt:
		return &b
	default:
		return &a
	}
}

func upsertConsensusRows(db *gorm.DB, rows []store.ShadowSnapshot) error {
	if len(rows) == 0 {
		return nil
	}

	assignments := map[string]interface{}{
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
	}

	return db.Transaction(func(tx *gorm.DB) error {
		const batchSize = 500
		for start := 0; start < len(rows); start += batchSize {
			end := start + batchSize
			if end > len(rows) {
				end = len(rows)
			}
			batch := rows[start:end]
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "trader_id"},
					{Name: "decision_time"},
					{Name: "symbol"},
				},
				DoUpdates: clause.Assignments(assignments),
			}).Create(&batch).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func verifyAdaptiveState(cfg store.DBConfig) error {
	st, err := store.NewWithConfig(cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	market.SetAdaptiveWeightStore(st)
	state := market.GetAdaptiveWeightState("", "", "")
	log.Printf(
		"adaptive global state: sample_count=%d sample_target=%d blend_adaptive=%.3f blend_default=%.3f",
		state.SampleCount,
		state.SampleTarget,
		state.BlendAdaptive,
		state.BlendDefault,
	)
	return nil
}

func defaultEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func defaultEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minPositive(a, b int64) int64 {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}
