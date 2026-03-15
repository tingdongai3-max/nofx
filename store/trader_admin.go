package store

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// TraderAdmin manages AI traders with hallucination detection and strategy optimization
type TraderAdmin struct {
	ID                string    `gorm:"primaryKey" json:"id"`
	UserID            string    `gorm:"column:user_id;not null;index" json:"user_id"`
	Name              string    `gorm:"column:name;not null" json:"name"`
	AIModelID         string    `gorm:"column:ai_model_id;not null" json:"ai_model_id"`
	ManagedTraderIDs  string    `gorm:"column:managed_trader_ids;not null"` // JSON array of trader IDs
	ScanIntervalMins  int       `gorm:"column:scan_interval_mins;default:60" json:"scan_interval_mins"`
	IsRunning         bool      `gorm:"column:is_running;default:false" json:"is_running"`
	LastScanTime      time.Time `gorm:"column:last_scan_time" json:"last_scan_time"`
	CreatedAt         time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// TableName returns the table name
func (TraderAdmin) TableName() string { return "trader_admins" }

// GetManagedTraderIDs returns the managed trader IDs as a slice
func (ta *TraderAdmin) GetManagedTraderIDs() []string {
	var ids []string
	if ta.ManagedTraderIDs != "" {
		json.Unmarshal([]byte(ta.ManagedTraderIDs), &ids)
	}
	return ids
}

// SetManagedTraderIDs sets the managed trader IDs from a slice
func (ta *TraderAdmin) SetManagedTraderIDs(ids []string) {
	data, _ := json.Marshal(ids)
	ta.ManagedTraderIDs = string(data)
}

// TraderAdminStore handles CRUD operations for TraderAdmin
type TraderAdminStore struct {
	db *gorm.DB
}

// NewTraderAdminStore creates a new TraderAdminStore
func NewTraderAdminStore(db *gorm.DB) *TraderAdminStore {
	return &TraderAdminStore{db: db}
}

// initTables initializes the trader_admins table
func (s *TraderAdminStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'trader_admins'`).Scan(&tableExists)
		if tableExists > 0 {
			return nil
		}
	}
	return s.db.AutoMigrate(&TraderAdmin{})
}

// Create creates a new TraderAdmin
func (s *TraderAdminStore) Create(admin *TraderAdmin) error {
	if admin.ID == "" {
		admin.ID = generateAdminUUID()
	}
	if admin.CreatedAt.IsZero() {
		admin.CreatedAt = time.Now().UTC()
	}
	if admin.UpdatedAt.IsZero() {
		admin.UpdatedAt = time.Now().UTC()
	}
	return s.db.Create(admin).Error
}

// List returns all TraderAdmins for a user
func (s *TraderAdminStore) List(userID string) ([]*TraderAdmin, error) {
	var admins []*TraderAdmin
	err := s.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&admins).Error
	return admins, err
}

// GetByID returns a TraderAdmin by ID
func (s *TraderAdminStore) GetByID(id string) (*TraderAdmin, error) {
	var admin TraderAdmin
	err := s.db.Where("id = ?", id).First(&admin).Error
	if err != nil {
		return nil, err
	}
	return &admin, nil
}

// Update updates a TraderAdmin
func (s *TraderAdminStore) Update(admin *TraderAdmin) error {
	admin.UpdatedAt = time.Now().UTC()
	return s.db.Save(admin).Error
}

// Delete deletes a TraderAdmin
func (s *TraderAdminStore) Delete(id string) error {
	return s.db.Where("id = ?", id).Delete(&TraderAdmin{}).Error
}

// UpdateStatus updates the running status of a TraderAdmin
func (s *TraderAdminStore) UpdateStatus(id string, isRunning bool) error {
	return s.db.Model(&TraderAdmin{}).Where("id = ?", id).Updates(map[string]interface{}{
		"is_running":     isRunning,
		"last_scan_time": time.Now().UTC(),
	}).Error
}

// UpdateLastScanTime updates the last scan time
func (s *TraderAdminStore) UpdateLastScanTime(id string) error {
	return s.db.Model(&TraderAdmin{}).Where("id = ?", id).Update("last_scan_time", time.Now().UTC()).Error
}

// GetRunningAdmins returns all running TraderAdmins
func (s *TraderAdminStore) GetRunningAdmins() ([]*TraderAdmin, error) {
	var admins []*TraderAdmin
	err := s.db.Where("is_running = ?", true).Find(&admins).Error
	return admins, err
}

// GetAll returns all TraderAdmins (for admin users)
func (s *TraderAdminStore) GetAll() ([]*TraderAdmin, error) {
	var admins []*TraderAdmin
	err := s.db.Order("created_at DESC").Find(&admins).Error
	return admins, err
}

// ============================================================================
// Analysis Result Storage
// ============================================================================

// TraderAdminAnalysis stores analysis results for a TraderAdmin scan
type TraderAdminAnalysis struct {
	ID                string    `gorm:"primaryKey" json:"id"`
	AdminID           string    `gorm:"column:admin_id;not null;index" json:"admin_id"`
	TraderID          string    `gorm:"column:trader_id;not null;index" json:"trader_id"`
	ScanTime          time.Time `gorm:"column:scan_time;not null" json:"scan_time"`
	AnalysisData      string    `gorm:"column:analysis_data;type:text"` // JSON string of analysis result
	HallucinationData string    `gorm:"column:hallucination_data;type:text"` // JSON string of hallucination report
	Optimizations     string    `gorm:"column:optimizations;type:text"` // JSON string of optimization suggestions
	CreatedAt         time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

// TableName returns the table name
func (TraderAdminAnalysis) TableName() string { return "trader_admin_analyses" }

// TraderAdminAnalysisStore handles CRUD operations for TraderAdminAnalysis
type TraderAdminAnalysisStore struct {
	db *gorm.DB
}

// NewTraderAdminAnalysisStore creates a new TraderAdminAnalysisStore
func NewTraderAdminAnalysisStore(db *gorm.DB) *TraderAdminAnalysisStore {
	return &TraderAdminAnalysisStore{db: db}
}

// initTables initializes the trader_admin_analyses table
func (s *TraderAdminAnalysisStore) initTables() error {
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'trader_admin_analyses'`).Scan(&tableExists)
		if tableExists > 0 {
			return nil
		}
	}
	return s.db.AutoMigrate(&TraderAdminAnalysis{})
}

// Create creates a new analysis record
func (s *TraderAdminAnalysisStore) Create(analysis *TraderAdminAnalysis) error {
	if analysis.ID == "" {
		analysis.ID = generateAdminUUID()
	}
	if analysis.CreatedAt.IsZero() {
		analysis.CreatedAt = time.Now().UTC()
	}
	return s.db.Create(analysis).Error
}

// GetByAdminID returns all analyses for an admin
func (s *TraderAdminAnalysisStore) GetByAdminID(adminID string, limit int) ([]*TraderAdminAnalysis, error) {
	var analyses []*TraderAdminAnalysis
	err := s.db.Where("admin_id = ?", adminID).Order("scan_time DESC").Limit(limit).Find(&analyses).Error
	return analyses, err
}

// GetLatestByAdminID returns the latest analysis for an admin
func (s *TraderAdminAnalysisStore) GetLatestByAdminID(adminID string) (*TraderAdminAnalysis, error) {
	var analysis TraderAdminAnalysis
	err := s.db.Where("admin_id = ?", adminID).Order("scan_time DESC").First(&analysis).Error
	if err != nil {
		return nil, err
	}
	return &analysis, nil
}

// DeleteByAdminID deletes all analyses for an admin
func (s *TraderAdminAnalysisStore) DeleteByAdminID(adminID string) error {
	return s.db.Where("admin_id = ?", adminID).Delete(&TraderAdminAnalysis{}).Error
}

// Helper function to generate UUID
func generateAdminUUID() string {
	return fmt.Sprintf("%d-%d-%d", time.Now().UnixNano(), randomAdminInt(1000, 9999), randomAdminInt(1000, 9999))
}

func randomAdminInt(min, max int) int {
	return min + int(time.Now().UnixNano()%int64(max-min))
}
