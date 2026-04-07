package store

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ReplayFixtureStore stores versioned validation fixtures for replay, conflict, and audit scenarios.
// These rows are validation-only assets and do not participate in live execution paths directly.
type ReplayFixtureStore struct {
	db *gorm.DB
}

// NewReplayFixtureStore creates a replay fixture store.
func NewReplayFixtureStore(db *gorm.DB) *ReplayFixtureStore {
	return &ReplayFixtureStore{db: db}
}

// ReplayFixture is the versioned validation fixture truth row used by the phase-7 replay bus.
// It stores replay-state inputs, not reconciled trading truth.
type ReplayFixture struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`                                                                            // System persistence row id for the validation fixture.
	FixtureID   string    `gorm:"column:fixture_id;not null;default:'';uniqueIndex:idx_replay_fixtures_fixture_id" json:"fixture_id"`            // Stable validation fixture identifier.
	FixtureType string    `gorm:"column:fixture_type;not null;default:'';index:idx_replay_fixtures_type_version,priority:1" json:"fixture_type"` // Validation fixture bucket: user_stream, conflict_matrix, exchange_snapshot, sqlite_fixture.
	Version     string    `gorm:"column:version;not null;default:'';index:idx_replay_fixtures_type_version,priority:2" json:"version"`           // Fixture contract version used by replay/restore tooling.
	Source      string    `gorm:"column:source;not null;default:''" json:"source"`                                                               // Fixture source label: recorded, synthetic, bootstrap.
	PayloadJSON string    `gorm:"column:payload_json;type:text;not null;default:''" json:"payload_json"`                                         // Raw fixture payload used by validation runners.
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`                                                            // Persistence timestamp.
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`                                                            // Persistence timestamp.
}

// TableName returns the table name for ReplayFixture.
func (ReplayFixture) TableName() string {
	return "replay_fixtures"
}

// initTables initializes the replay fixture table and indexes.
func (s *ReplayFixtureStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'replay_fixtures'`).Scan(&tableExists)
		if tableExists > 0 {
			return s.ensureIndexes()
		}
	}

	if err := s.db.AutoMigrate(&ReplayFixture{}); err != nil {
		return fmt.Errorf("failed to migrate replay_fixtures table: %w", err)
	}

	return s.ensureIndexes()
}

func (s *ReplayFixtureStore) ensureIndexes() error {
	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_replay_fixtures_fixture_id ON replay_fixtures(fixture_id)`,
		`CREATE INDEX IF NOT EXISTS idx_replay_fixtures_type_version ON replay_fixtures(fixture_type, version)`,
	}
	for _, query := range indexes {
		if err := s.db.Exec(query).Error; err != nil {
			return fmt.Errorf("failed to create replay fixture index: %w", err)
		}
	}
	return nil
}

// Upsert stores or updates a replay fixture row by fixture id.
func (s *ReplayFixtureStore) Upsert(row *ReplayFixture) error {
	if row == nil {
		return fmt.Errorf("replay fixture row is nil")
	}
	row.FixtureID = strings.TrimSpace(row.FixtureID)
	row.FixtureType = strings.TrimSpace(row.FixtureType)
	row.Version = strings.TrimSpace(row.Version)
	row.Source = strings.TrimSpace(row.Source)
	if row.FixtureID == "" {
		return fmt.Errorf("fixture ID is required")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var existing ReplayFixture
		err := tx.Where("fixture_id = ?", row.FixtureID).First(&existing).Error
		switch {
		case err == nil:
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			return tx.Save(row).Error
		case err == gorm.ErrRecordNotFound:
			return tx.Create(row).Error
		default:
			return fmt.Errorf("failed to upsert replay fixture row: %w", err)
		}
	})
}

// GetByFixtureID loads a replay fixture row by fixture id.
func (s *ReplayFixtureStore) GetByFixtureID(fixtureID string) (*ReplayFixture, error) {
	var row ReplayFixture
	err := s.db.Where("fixture_id = ?", strings.TrimSpace(fixtureID)).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get replay fixture row: %w", err)
	}
	return &row, nil
}

// ListByType returns replay fixtures for one fixture type ordered by version and creation time.
func (s *ReplayFixtureStore) ListByType(fixtureType string) ([]*ReplayFixture, error) {
	var rows []*ReplayFixture
	err := s.db.Where("fixture_type = ?", strings.TrimSpace(fixtureType)).
		Order("version DESC, created_at DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list replay fixtures: %w", err)
	}
	return rows, nil
}
