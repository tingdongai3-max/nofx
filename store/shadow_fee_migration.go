package store

import (
	"fmt"
	"strings"

	"nofx/logger"

	"gorm.io/gorm"
)

const ShadowRoundTripFeeRate = 0.0015

const shadowRoundTripFeeMigrationKey = "shadow_round_trip_fee_migration_v1"

// ApplyShadowRoundTripFeeMigration subtracts the round-trip taker fee from all
// filled shadow snapshots exactly once. It is safe to call on every startup.
func ApplyShadowRoundTripFeeMigration(st *Store) (int64, error) {
	if st == nil || st.GormDB() == nil {
		return 0, nil
	}

	var migrated int64
	err := st.Transaction(func(tx *gorm.DB) error {
		var marker string
		if err := tx.Raw("SELECT value FROM system_config WHERE key = ?", shadowRoundTripFeeMigrationKey).Scan(&marker).Error; err != nil {
			return fmt.Errorf("load shadow round-trip fee migration marker: %w", err)
		}
		if strings.TrimSpace(marker) == "1" {
			return nil
		}

		result := tx.Exec(
			`UPDATE shadow_snapshots SET return_pct = return_pct - ? WHERE filled = ?`,
			ShadowRoundTripFeeRate,
			true,
		)
		if result.Error != nil {
			return fmt.Errorf("apply shadow round-trip fee migration: %w", result.Error)
		}
		migrated = result.RowsAffected

		if err := tx.Exec(`
			INSERT INTO system_config (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`, shadowRoundTripFeeMigrationKey, "1").Error; err != nil {
			return fmt.Errorf("persist shadow round-trip fee migration marker: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	if migrated > 0 {
		logger.Infof("✅ Applied shadow round-trip fee migration to %d filled shadow snapshots", migrated)
	}
	return migrated, nil
}
