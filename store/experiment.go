package store

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Experiment struct {
	ID              string    `gorm:"primaryKey" json:"id"`
	UserID          string    `gorm:"column:user_id;not null;index" json:"user_id"`
	MasterTraderID  string    `gorm:"column:master_trader_id;not null;index" json:"master_trader_id"`
	ShadowTraderIDs string    `gorm:"column:shadow_trader_ids;type:text;not null" json:"-"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Experiment) TableName() string {
	return "experiments"
}

type ExperimentRecord struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	MasterTraderID  string    `json:"master_trader_id"`
	ShadowTraderIDs []string  `json:"shadow_trader_ids"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ExperimentStore struct {
	db *gorm.DB
}

func NewExperimentStore(db *gorm.DB) *ExperimentStore {
	return &ExperimentStore{db: db}
}

func (s *ExperimentStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'experiments'`).Scan(&tableExists)
		if tableExists > 0 {
			return nil
		}
	}
	return s.db.AutoMigrate(&Experiment{})
}

func (s *ExperimentStore) Create(userID, masterTraderID string, shadowTraderIDs []string) (*ExperimentRecord, error) {
	payload, err := json.Marshal(shadowTraderIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal shadow trader ids: %w", err)
	}

	exp := &Experiment{
		ID:              fmt.Sprintf("exp_%d", time.Now().UTC().UnixNano()),
		UserID:          userID,
		MasterTraderID:  masterTraderID,
		ShadowTraderIDs: string(payload),
	}
	if err := s.db.Create(exp).Error; err != nil {
		return nil, fmt.Errorf("failed to create experiment: %w", err)
	}
	return exp.toRecord(), nil
}

func (s *ExperimentStore) ListByUser(userID string) ([]*ExperimentRecord, error) {
	var experiments []*Experiment
	if err := s.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&experiments).Error; err != nil {
		return nil, fmt.Errorf("failed to list experiments: %w", err)
	}
	result := make([]*ExperimentRecord, 0, len(experiments))
	for _, exp := range experiments {
		result = append(result, exp.toRecord())
	}
	return result, nil
}

func (s *ExperimentStore) ListAll() ([]*ExperimentRecord, error) {
	var experiments []*Experiment
	if err := s.db.Order("created_at DESC").Find(&experiments).Error; err != nil {
		return nil, fmt.Errorf("failed to list experiments: %w", err)
	}
	result := make([]*ExperimentRecord, 0, len(experiments))
	for _, exp := range experiments {
		result = append(result, exp.toRecord())
	}
	return result, nil
}

func (e *Experiment) toRecord() *ExperimentRecord {
	record := &ExperimentRecord{
		ID:             e.ID,
		UserID:         e.UserID,
		MasterTraderID: e.MasterTraderID,
		CreatedAt:      e.CreatedAt,
		UpdatedAt:      e.UpdatedAt,
	}
	_ = json.Unmarshal([]byte(e.ShadowTraderIDs), &record.ShadowTraderIDs)
	return record
}
