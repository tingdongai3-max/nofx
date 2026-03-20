package store

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type ExperimentLog struct {
	ID                int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ExperimentID      string    `gorm:"column:experiment_id;not null;index" json:"experiment_id"`
	UserID            string    `gorm:"column:user_id;not null;index" json:"user_id"`
	CoachModelID      string    `gorm:"column:coach_model_id;not null;default:''" json:"coach_model_id"`
	CoachReasoning    string    `gorm:"column:coach_reasoning;type:text;not null;default:''" json:"coach_reasoning"`
	WinnerTraderIDs   string    `gorm:"column:winner_trader_ids;type:text;not null;default:'[]'" json:"-"`
	LoserTraderIDs    string    `gorm:"column:loser_trader_ids;type:text;not null;default:'[]'" json:"-"`
	ReplacedTraderIDs string    `gorm:"column:replaced_trader_ids;type:text;not null;default:'[]'" json:"-"`
	OldPrompt         string    `gorm:"column:old_prompt;type:text;not null;default:''" json:"old_prompt"`
	NewPrompt         string    `gorm:"column:new_prompt;type:text;not null;default:''" json:"new_prompt"`
	PromptDiff        string    `gorm:"column:prompt_diff;type:text;not null;default:''" json:"prompt_diff"`
	Summary           string    `gorm:"column:summary;type:text;not null;default:''" json:"summary"`
	CreatedAt         time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (ExperimentLog) TableName() string {
	return "experiment_logs"
}

type ExperimentLogRecord struct {
	ID                int64     `json:"id"`
	ExperimentID      string    `json:"experiment_id"`
	UserID            string    `json:"user_id"`
	CoachModelID      string    `json:"coach_model_id"`
	CoachReasoning    string    `json:"coach_reasoning"`
	WinnerTraderIDs   []string  `json:"winner_trader_ids"`
	LoserTraderIDs    []string  `json:"loser_trader_ids"`
	ReplacedTraderIDs []string  `json:"replaced_trader_ids"`
	OldPrompt         string    `json:"old_prompt"`
	NewPrompt         string    `json:"new_prompt"`
	PromptDiff        string    `json:"prompt_diff"`
	Summary           string    `json:"summary"`
	CreatedAt         time.Time `json:"created_at"`
}

type ExperimentLogStore struct {
	db *gorm.DB
}

func NewExperimentLogStore(db *gorm.DB) *ExperimentLogStore {
	return &ExperimentLogStore{db: db}
}

func (s *ExperimentLogStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'experiment_logs'`).Scan(&tableExists)
		if tableExists > 0 {
			var columnExists int64
			s.db.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'experiment_logs' AND column_name = 'coach_reasoning'`).Scan(&columnExists)
			if columnExists == 0 {
				s.db.Exec(`ALTER TABLE experiment_logs ADD COLUMN coach_reasoning TEXT NOT NULL DEFAULT ''`)
			}
			return nil
		}
	}
	if err := s.db.AutoMigrate(&ExperimentLog{}); err != nil {
		return err
	}
	if s.db.Dialector.Name() != "postgres" {
		var columnExists int64
		s.db.Raw(`SELECT COUNT(*) FROM pragma_table_info('experiment_logs') WHERE name = ?`, "coach_reasoning").Scan(&columnExists)
		if columnExists == 0 {
			s.db.Exec(`ALTER TABLE experiment_logs ADD COLUMN coach_reasoning TEXT NOT NULL DEFAULT ''`)
		}
	}
	return nil
}

func (s *ExperimentLogStore) Create(record *ExperimentLogRecord) error {
	winners, _ := json.Marshal(record.WinnerTraderIDs)
	losers, _ := json.Marshal(record.LoserTraderIDs)
	replaced, _ := json.Marshal(record.ReplacedTraderIDs)
	dbRecord := &ExperimentLog{
		ExperimentID:      record.ExperimentID,
		UserID:            record.UserID,
		CoachModelID:      record.CoachModelID,
		CoachReasoning:    record.CoachReasoning,
		WinnerTraderIDs:   string(winners),
		LoserTraderIDs:    string(losers),
		ReplacedTraderIDs: string(replaced),
		OldPrompt:         record.OldPrompt,
		NewPrompt:         record.NewPrompt,
		PromptDiff:        record.PromptDiff,
		Summary:           record.Summary,
	}
	if err := s.db.Create(dbRecord).Error; err != nil {
		return fmt.Errorf("failed to create experiment log: %w", err)
	}
	record.ID = dbRecord.ID
	record.CreatedAt = dbRecord.CreatedAt
	return nil
}

func (s *ExperimentLogStore) ListRecentByExperiment(experimentID string, limit int) ([]*ExperimentLogRecord, error) {
	if limit <= 0 {
		limit = 8
	}

	var rows []*ExperimentLog
	if err := s.db.Where("experiment_id = ?", experimentID).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to query experiment logs: %w", err)
	}

	out := make([]*ExperimentLogRecord, 0, len(rows))
	for _, row := range rows {
		record := &ExperimentLogRecord{
			ID:             row.ID,
			ExperimentID:   row.ExperimentID,
			UserID:         row.UserID,
			CoachModelID:   row.CoachModelID,
			CoachReasoning: row.CoachReasoning,
			OldPrompt:      row.OldPrompt,
			NewPrompt:      row.NewPrompt,
			PromptDiff:     row.PromptDiff,
			Summary:        row.Summary,
			CreatedAt:      row.CreatedAt,
		}
		_ = json.Unmarshal([]byte(row.WinnerTraderIDs), &record.WinnerTraderIDs)
		_ = json.Unmarshal([]byte(row.LoserTraderIDs), &record.LoserTraderIDs)
		_ = json.Unmarshal([]byte(row.ReplacedTraderIDs), &record.ReplacedTraderIDs)
		out = append(out, record)
	}
	return out, nil
}
