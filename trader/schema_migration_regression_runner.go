package trader

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"

	_ "modernc.org/sqlite"
)

// MigrationRegressionPreview is the validation result produced by the schema migration regression runner.
// It is validation-only and does not add live trading behavior.
type MigrationRegressionPreview struct {
	TruthSnapshotMetadata
	FixtureID          string                     `json:"fixture_id"`                     // Stable schema migration fixture identifier.
	Phase              string                     `json:"phase"`                          // Legacy phase represented by the fixture.
	FixtureVersion     string                     `json:"fixture_version"`                // Schema migration fixture contract version.
	ValidationTraderID string                     `json:"validation_trader_id,omitempty"` // Trader scope used for post-migration truth validation.
	SelectedSymbol     string                     `json:"selected_symbol,omitempty"`      // Selected symbol scope used for post-migration validation.
	TruthSnapshot      *TruthSnapshotSummary      `json:"truth_snapshot,omitempty"`       // Truth snapshot rebuilt from the migrated schema.
	CapabilityPreview  *RuntimeCapabilityPreview  `json:"capability_preview,omitempty"`   // Capability result rebuilt from the migrated schema when a trader scope exists.
	HasMismatch        bool                       `json:"has_mismatch"`                   // Validation flag showing that the migrated schema is inconsistent.
	MismatchReasons    []ValidationMismatchReason `json:"mismatch_reasons"`               // Machine-readable migration mismatch explanations.
	GeneratedAt        time.Time                  `json:"generated_at"`                   // Validation result generation timestamp.
}

type schemaMigrationFixturePayload struct {
	SchemaSQL           []string `json:"schema_sql"`
	SeedSQL             []string `json:"seed_sql"`
	ValidationTraderID  string   `json:"validation_trader_id"`
	ValidationUserID    string   `json:"validation_user_id"`
	SelectedSymbol      string   `json:"selected_symbol"`
	ExecutionMode       string   `json:"execution_mode"`
	UserStreamReady     bool     `json:"user_stream_ready"`
	AllowOrderPlacement bool     `json:"allow_order_placement"`
}

// SchemaMigrationRegressionRunner upgrades legacy SQLite fixtures to the current schema and validates the truth layers.
// It is validation-only and must not be used as a live execution path.
type SchemaMigrationRegressionRunner struct{}

// NewSchemaMigrationRegressionRunner creates a schema migration regression runner.
func NewSchemaMigrationRegressionRunner() *SchemaMigrationRegressionRunner {
	return &SchemaMigrationRegressionRunner{}
}

// RunFixture upgrades one legacy SQLite fixture to the current schema and validates the rebuilt truth layers.
func (r *SchemaMigrationRegressionRunner) RunFixture(rootStore *store.Store, fixtureID string) (*MigrationRegressionPreview, error) {
	if rootStore == nil {
		return nil, fmt.Errorf("root store is required")
	}
	fixtureID = strings.TrimSpace(fixtureID)
	if fixtureID == "" {
		return nil, fmt.Errorf("fixture ID is required")
	}

	fixtureStore, err := openPhase7ValidationFixtureStore()
	if err != nil {
		return nil, err
	}
	fixture, err := fixtureStore.SchemaMigrationFixture().GetByFixtureID(fixtureID)
	if err != nil {
		return nil, err
	}
	if fixture == nil {
		fixture, err = rootStore.SchemaMigrationFixture().GetByFixtureID(fixtureID)
		if err != nil {
			return nil, err
		}
		if fixture == nil {
			return nil, fmt.Errorf("schema migration fixture %s not found", fixtureID)
		}
	}

	var payload schemaMigrationFixturePayload
	if err := json.Unmarshal([]byte(fixture.PayloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("failed to parse schema migration fixture payload: %w", err)
	}

	logger.Infof("schema migration regression started: fixture_id=%s phase=%s version=%s", fixture.FixtureID, fixture.Phase, fixture.Version)

	tempDir, err := os.MkdirTemp("", "nofx-phase7-migration-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "migration-regression.sqlite")
	if err := seedLegacySQLiteFixture(dbPath, payload.SchemaSQL, payload.SeedSQL); err != nil {
		return nil, err
	}

	migratedStore, err := store.New(dbPath)
	if err != nil {
		return nil, err
	}

	mismatches := make([]ValidationMismatchReason, 0)
	if payload.ValidationTraderID != "" {
		if _, err := store.NewPositionAggregateBuilder(migratedStore).BuildForTrader(payload.ValidationTraderID, nil, nil); err != nil {
			return nil, err
		}
	}

	truthSnapshot, err := BuildTruthSnapshotSummary(migratedStore, payload.ValidationTraderID, payload.SelectedSymbol, payload.UserStreamReady)
	if err != nil && payload.ValidationTraderID != "" {
		return nil, err
	}

	var capabilityPreview *RuntimeCapabilityPreview
	if payload.ValidationTraderID != "" && payload.ValidationUserID != "" {
		capabilityPreview, err = BuildRuntimeCapabilityPreview(
			migratedStore,
			payload.ValidationUserID,
			payload.ValidationTraderID,
			payload.SelectedSymbol,
			chooseNonEmpty(strings.TrimSpace(payload.ExecutionMode), "readonly"),
			nil,
			nil,
			nil,
			nil,
			nil,
			payload.AllowOrderPlacement,
			nil,
		)
		if err != nil {
			return nil, err
		}
	}

	mismatches = append(mismatches, validateMigrationTruthIntegrity(migratedStore, payload.ValidationTraderID, payload.SelectedSymbol)...)

	now := time.Now().UTC()
	metadata := buildTruthSnapshotMetadata(now, "", false, fixture.Version)
	if truthSnapshot != nil {
		truthSnapshot.TruthSnapshotMetadata = metadata
	}
	if capabilityPreview != nil {
		capabilityPreview.TruthSnapshotMetadata = metadata
	}

	quantityStatus := "unknown"
	quantityReasons := 0
	if truthSnapshot != nil && truthSnapshot.QuantityAudit != nil {
		quantityStatus = truthSnapshot.QuantityAudit.Status
		quantityReasons = len(truthSnapshot.QuantityAudit.Reasons)
	}
	logger.Infof("schema migration regression completed: fixture_id=%s phase=%s version=%s mismatch_count=%d quantity_status=%s quantity_reasons=%d", fixture.FixtureID, fixture.Phase, fixture.Version, len(mismatches), quantityStatus, quantityReasons)

	return &MigrationRegressionPreview{
		TruthSnapshotMetadata: metadata,
		FixtureID:             fixture.FixtureID,
		Phase:                 fixture.Phase,
		FixtureVersion:        fixture.Version,
		ValidationTraderID:    payload.ValidationTraderID,
		SelectedSymbol:        payload.SelectedSymbol,
		TruthSnapshot:         truthSnapshot,
		CapabilityPreview:     capabilityPreview,
		HasMismatch:           len(mismatches) > 0,
		MismatchReasons:       mismatches,
		GeneratedAt:           now,
	}, nil
}

func seedLegacySQLiteFixture(dbPath string, schemaSQL, seedSQL []string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to open legacy sqlite fixture: %w", err)
	}
	defer db.Close()

	for _, statement := range schemaSQL {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("failed to execute legacy schema statement: %w", err)
		}
	}
	for _, statement := range seedSQL {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("failed to execute legacy seed statement: %w", err)
		}
	}
	return nil
}

func validateMigrationTruthIntegrity(st *store.Store, traderID, selectedSymbol string) []ValidationMismatchReason {
	reasons := make([]ValidationMismatchReason, 0)
	if st == nil || traderID == "" {
		return reasons
	}

	aggregates, err := st.PositionAggregate().ListByTraderID(traderID)
	if err != nil {
		return append(reasons, ValidationMismatchReason{
			Category: "migration",
			Reason:   "failed to load rebuilt position aggregates after migration",
			Field:    "position_aggregates",
			Actual:   err.Error(),
		})
	}

	for _, aggregate := range aggregates {
		if aggregate == nil {
			continue
		}
		if aggregate.HasProtection && strings.TrimSpace(aggregate.ProtectionGroupID) == "" {
			reasons = append(reasons, ValidationMismatchReason{
				Category: "migration",
				Reason:   "aggregate reports protection but protection_group_id is empty",
				Field:    "protection_group_id",
				Symbol:   aggregate.Symbol,
			})
		}
		if aggregate.HasScaleOutPlan && strings.TrimSpace(aggregate.ScaleOutStatus) == "" {
			reasons = append(reasons, ValidationMismatchReason{
				Category: "migration",
				Reason:   "aggregate reports scale-out plan but scale_out_status is empty",
				Field:    "scale_out_status",
				Symbol:   aggregate.Symbol,
			})
		}
		if aggregate.HasScaleInPlan && strings.TrimSpace(aggregate.ScaleInStatus) == "" {
			reasons = append(reasons, ValidationMismatchReason{
				Category: "migration",
				Reason:   "aggregate reports scale-in plan but scale_in_status is empty",
				Field:    "scale_in_status",
				Symbol:   aggregate.Symbol,
			})
		}
	}

	if selectedSymbol != "" {
		rows, err := st.OrderRegistry().ListByTraderSymbol(traderID, selectedSymbol)
		if err != nil {
			return append(reasons, ValidationMismatchReason{
				Category: "migration",
				Reason:   "failed to load order registry rows after migration",
				Field:    "order_registry",
				Symbol:   selectedSymbol,
				Actual:   err.Error(),
			})
		}
		for _, row := range rows {
			if row == nil {
				continue
			}
			if row.LinkedGroupID != "" && row.OrderRole == "stop_loss" {
				group, groupErr := st.ProtectionGroup().GetByKeys(traderID, row.LinkedGroupID, row.LinkedPositionKey)
				if groupErr != nil {
					return append(reasons, ValidationMismatchReason{
						Category: "migration",
						Reason:   "failed to validate linked protection group after migration",
						Field:    "linked_group_id",
						Symbol:   selectedSymbol,
						Actual:   groupErr.Error(),
					})
				}
				if group == nil {
					reasons = append(reasons, ValidationMismatchReason{
						Category: "migration",
						Reason:   "order registry contains orphan protection reference after migration",
						Field:    "linked_group_id",
						Symbol:   selectedSymbol,
						Actual:   row.LinkedGroupID,
					})
				}
			}
		}
	}

	return reasons
}
