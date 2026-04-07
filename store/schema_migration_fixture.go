package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SchemaMigrationFixtureStore stores versioned SQLite upgrade fixtures used by phase-7 regression runners.
// These rows are validation-only assets and do not alter live trading behavior directly.
type SchemaMigrationFixtureStore struct {
	db *gorm.DB
}

// NewSchemaMigrationFixtureStore creates a schema migration fixture store.
func NewSchemaMigrationFixtureStore(db *gorm.DB) *SchemaMigrationFixtureStore {
	return &SchemaMigrationFixtureStore{db: db}
}

// SchemaMigrationFixture is the versioned validation fixture used to replay legacy SQLite upgrades.
// It stores migration-state inputs, not reconciled trading truth.
type SchemaMigrationFixture struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                             // System persistence row id for the migration validation fixture.
	FixtureID   string    `gorm:"column:fixture_id;not null;default:'';uniqueIndex:idx_schema_migration_fixtures_fixture_id" json:"fixture_id"`   // Stable migration fixture identifier.
	Phase       string    `gorm:"column:phase;not null;default:'';index:idx_schema_migration_fixtures_phase_version,priority:1" json:"phase"`     // Legacy phase label represented by this fixture.
	Version     string    `gorm:"column:version;not null;default:'';index:idx_schema_migration_fixtures_phase_version,priority:2" json:"version"` // Fixture contract version used by the regression runner.
	Source      string    `gorm:"column:source;not null;default:''" json:"source"`                                                                // Fixture source label: synthetic, recorded, exported.
	PayloadJSON string    `gorm:"column:payload_json;type:text;not null;default:''" json:"payload_json"`                                          // Raw migration payload containing schema SQL, seed SQL, and validation expectations.
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                             // Persistence timestamp.
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                             // Persistence timestamp.
}

// TableName returns the table name for SchemaMigrationFixture.
func (SchemaMigrationFixture) TableName() string {
	return "schema_migration_fixtures"
}

// initTables initializes the schema migration fixture table and indexes.
func (s *SchemaMigrationFixtureStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'schema_migration_fixtures'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&SchemaMigrationFixture{}); err != nil {
		return fmt.Errorf("failed to migrate schema_migration_fixtures table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *SchemaMigrationFixtureStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_schema_migration_fixtures_fixture_id ON schema_migration_fixtures(fixture_id)`,
		`CREATE INDEX IF NOT EXISTS idx_schema_migration_fixtures_phase_version ON schema_migration_fixtures(phase, version)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create schema migration fixture index: %w", err)
		}
	}
	return nil
}

// Upsert stores or updates a schema migration fixture row by fixture id.
func (s *SchemaMigrationFixtureStore) Upsert(row *SchemaMigrationFixture) error {
	if row == nil {
		return fmt.Errorf("schema migration fixture row is nil")
	}
	row.FixtureID = strings.TrimSpace(row.FixtureID)
	row.Phase = strings.TrimSpace(row.Phase)
	row.Version = strings.TrimSpace(row.Version)
	row.Source = strings.TrimSpace(row.Source)
	if row.FixtureID == "" {
		return fmt.Errorf("fixture ID is required")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var existing SchemaMigrationFixture
		err := tx.Where("fixture_id = ?", row.FixtureID).First(&existing).Error
		switch {
		case err == nil:
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			return tx.Save(row).Error
		case err == gorm.ErrRecordNotFound:
			return tx.Create(row).Error
		default:
			return fmt.Errorf("failed to upsert schema migration fixture row: %w", err)
		}
	})
}

// GetByFixtureID loads a schema migration fixture row by fixture id.
func (s *SchemaMigrationFixtureStore) GetByFixtureID(fixtureID string) (*SchemaMigrationFixture, error) {
	var row SchemaMigrationFixture
	err := s.db.Where("fixture_id = ?", strings.TrimSpace(fixtureID)).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get schema migration fixture row: %w", err)
	}
	return &row, nil
}

// ListByPhase returns migration fixtures for one legacy phase ordered by version and creation time.
func (s *SchemaMigrationFixtureStore) ListByPhase(phase string) ([]*SchemaMigrationFixture, error) {
	var rows []*SchemaMigrationFixture
	err := s.db.Where("phase = ?", strings.TrimSpace(phase)).
		Order("version DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list schema migration fixtures: %w", err)
	}
	return rows, nil
}
