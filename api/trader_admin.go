package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"nofx/logger"
	"nofx/store"
	"nofx/trader"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// TraderAdminRequest represents a create/update trader admin request
type TraderAdminRequest struct {
	Name             string   `json:"name" binding:"required"`
	AIModelID        string   `json:"ai_model_id" binding:"required"`
	ManagedTraderIDs []string `json:"managed_trader_ids" binding:"required"`
	ScanIntervalMins int      `json:"scan_interval_mins"`
}

// TraderAdminResponse represents a trader admin response
type TraderAdminResponse struct {
	ID                string    `json:"id"`
	UserID            string    `json:"user_id"`
	Name              string    `json:"name"`
	AIModelID         string    `json:"ai_model_id"`
	ManagedTraderIDs  []string  `json:"managed_trader_ids"`
	ScanIntervalMins  int       `json:"scan_interval_mins"`
	IsRunning         bool      `json:"is_running"`
	LastScanTime      time.Time `json:"last_scan_time"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// toTraderAdminResponse converts store.TraderAdmin to API response
func toTraderAdminResponse(ta *store.TraderAdmin) TraderAdminResponse {
	return TraderAdminResponse{
		ID:                ta.ID,
		UserID:            ta.UserID,
		Name:              ta.Name,
		AIModelID:         ta.AIModelID,
		ManagedTraderIDs:  ta.GetManagedTraderIDs(),
		ScanIntervalMins:  ta.ScanIntervalMins,
		IsRunning:         ta.IsRunning,
		LastScanTime:      ta.LastScanTime,
		CreatedAt:         ta.CreatedAt,
		UpdatedAt:         ta.UpdatedAt,
	}
}

// handleCreateTraderAdmin creates a new trader admin
func (s *Server) handleCreateTraderAdmin(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		userID = "default"
	}

	var req TraderAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters: "+err.Error())
		return
	}

	if req.ScanIntervalMins == 0 {
		req.ScanIntervalMins = 60 // Default to 60 minutes
	}

	admin := &store.TraderAdmin{
		UserID:           userID,
		Name:             req.Name,
		AIModelID:        req.AIModelID,
		ScanIntervalMins: req.ScanIntervalMins,
		IsRunning:        false,
	}
	admin.SetManagedTraderIDs(req.ManagedTraderIDs)

	if err := s.store.TraderAdmin().Create(admin); err != nil {
		logger.Errorf("Failed to create trader admin: %v", err)
		SafeInternalError(c, "Failed to create trader admin", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result": toTraderAdminResponse(admin),
	})
}

// handleListTraderAdmins returns all trader admins for the user
func (s *Server) handleListTraderAdmins(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		userID = "default"
	}

	admins, err := s.store.TraderAdmin().List(userID)
	if err != nil {
		logger.Errorf("Failed to list trader admins: %v", err)
		SafeInternalError(c, "Failed to list trader admins", err)
		return
	}

	// Convert to response format
	response := make([]TraderAdminResponse, len(admins))
	for i, admin := range admins {
		response[i] = toTraderAdminResponse(admin)
	}

	c.JSON(http.StatusOK, gin.H{
		"result": response,
	})
}

// handleGetTraderAdmin returns a single trader admin
func (s *Server) handleGetTraderAdmin(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result": toTraderAdminResponse(admin),
	})
}

// handleUpdateTraderAdmin updates a trader admin
func (s *Server) handleUpdateTraderAdmin(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	var req TraderAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters: "+err.Error())
		return
	}

	admin.Name = req.Name
	admin.AIModelID = req.AIModelID
	admin.SetManagedTraderIDs(req.ManagedTraderIDs)
	if req.ScanIntervalMins > 0 {
		admin.ScanIntervalMins = req.ScanIntervalMins
	}

	if err := s.store.TraderAdmin().Update(admin); err != nil {
		logger.Errorf("Failed to update trader admin: %v", err)
		SafeInternalError(c, "Failed to update trader admin", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result": toTraderAdminResponse(admin),
	})
}

// handleDeleteTraderAdmin deletes a trader admin
func (s *Server) handleDeleteTraderAdmin(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	// Stop the admin first if running
	if admin, err := s.store.TraderAdmin().GetByID(id); err == nil {
		if admin.IsRunning {
			trader.GetTraderAdminRunner().StopScheduler(id)
			s.store.TraderAdmin().UpdateStatus(id, false)
		}
	}

	// Delete analysis records
	s.store.TraderAdminAnalysis().DeleteByAdminID(id)

	// Delete the admin
	if err := s.store.TraderAdmin().Delete(id); err != nil {
		logger.Errorf("Failed to delete trader admin: %v", err)
		SafeInternalError(c, "Failed to delete trader admin", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "success"})
}

// handleStartTraderAdmin starts the scheduler for a trader admin
func (s *Server) handleStartTraderAdmin(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	if admin.IsRunning {
		SafeBadRequest(c, "Trader admin is already running")
		return
	}

	// Start the scheduler
	runner := trader.GetTraderAdminRunner()
	if err := runner.StartScheduler(id, admin.ScanIntervalMins); err != nil {
		logger.Errorf("Failed to start trader admin scheduler: %v", err)
		SafeInternalError(c, "Failed to start scheduler", err)
		return
	}

	// Update status in database
	if err := s.store.TraderAdmin().UpdateStatus(id, true); err != nil {
		logger.Errorf("Failed to update trader admin status: %v", err)
		SafeInternalError(c, "Failed to update status", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "success"})
}

// handleStopTraderAdmin stops the scheduler for a trader admin
func (s *Server) handleStopTraderAdmin(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	if !admin.IsRunning {
		SafeBadRequest(c, "Trader admin is not running")
		return
	}

	// Stop the scheduler
	runner := trader.GetTraderAdminRunner()
	runner.StopScheduler(id)

	// Update status in database
	if err := s.store.TraderAdmin().UpdateStatus(id, false); err != nil {
		logger.Errorf("Failed to update trader admin status: %v", err)
		SafeInternalError(c, "Failed to update status", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "success"})
}

// handleGetTraderAdminAnalysis returns analysis results for a trader admin
func (s *Server) handleGetTraderAdminAnalysis(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 50
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	analyses, err := s.store.TraderAdminAnalysis().GetByAdminID(id, limit)
	if err != nil {
		logger.Errorf("Failed to get trader admin analyses: %v", err)
		SafeInternalError(c, "Failed to get analyses", err)
		return
	}

	// Parse analysis data
	type ParsedAnalysis struct {
		ID                 string          `json:"id"`
		AdminID            string          `json:"admin_id"`
		TraderID           string          `json:"trader_id"`
		ScanTime           time.Time       `json:"scan_time"`
		AnalysisData      json.RawMessage `json:"analysis_data"`
		HallucinationData json.RawMessage `json:"hallucination_data"`
		Optimizations      json.RawMessage `json:"optimizations"`
		CreatedAt          time.Time       `json:"created_at"`
	}

	result := make([]ParsedAnalysis, len(analyses))
	for i, a := range analyses {
		result[i] = ParsedAnalysis{
			ID:                 a.ID,
			AdminID:            a.AdminID,
			TraderID:           a.TraderID,
			ScanTime:           a.ScanTime,
			AnalysisData:      json.RawMessage(a.AnalysisData),
			HallucinationData: json.RawMessage(a.HallucinationData),
			Optimizations:     json.RawMessage(a.Optimizations),
			CreatedAt:          a.CreatedAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"result": result,
		"admin":  toTraderAdminResponse(admin),
	})
}

// handleGetAllTraders returns all traders (for admin to select)
func (s *Server) handleGetAllTraders(c *gin.Context) {
	traders, err := s.store.Trader().ListAll()
	if err != nil {
		logger.Errorf("Failed to list traders: %v", err)
		SafeInternalError(c, "Failed to list traders", err)
		return
	}

	type TraderListItem struct {
		TraderID   string `json:"trader_id"`
		TraderName string `json:"trader_name"`
		UserID     string `json:"user_id"`
		AIModelID  string `json:"ai_model_id"`
		IsRunning  bool   `json:"is_running"`
	}

	result := make([]TraderListItem, len(traders))
	for i, t := range traders {
		result[i] = TraderListItem{
			TraderID:   t.ID,
			TraderName: t.Name,
			UserID:     t.UserID,
			AIModelID:  t.AIModelID,
			IsRunning:  t.IsRunning,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"result": result,
	})
}

// handleGetTraderAdminLatestAnalysis returns the latest analysis for a trader admin
func (s *Server) handleGetTraderAdminLatestAnalysis(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	analysis, err := s.store.TraderAdminAnalysis().GetLatestByAdminID(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"result": nil,
			"admin":  toTraderAdminResponse(admin),
		})
		return
	}

	type ParsedAnalysis struct {
		ID                 string          `json:"id"`
		AdminID            string          `json:"admin_id"`
		TraderID           string          `json:"trader_id"`
		ScanTime           time.Time       `json:"scan_time"`
		AnalysisData      json.RawMessage `json:"analysis_data"`
		HallucinationData json.RawMessage `json:"hallucination_data"`
		Optimizations      json.RawMessage `json:"optimizations"`
		CreatedAt          time.Time       `json:"created_at"`
	}

	result := ParsedAnalysis{
		ID:                 analysis.ID,
		AdminID:            analysis.AdminID,
		TraderID:           analysis.TraderID,
		ScanTime:           analysis.ScanTime,
		AnalysisData:      json.RawMessage(analysis.AnalysisData),
		HallucinationData: json.RawMessage(analysis.HallucinationData),
		Optimizations:     json.RawMessage(analysis.Optimizations),
		CreatedAt:          analysis.CreatedAt,
	}

	c.JSON(http.StatusOK, gin.H{
		"result": result,
		"admin":  toTraderAdminResponse(admin),
	})
}

// handleTriggerTraderAdminScan triggers a manual scan for a trader admin
func (s *Server) handleTriggerTraderAdminScan(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		SafeBadRequest(c, "Admin ID is required")
		return
	}

	admin, err := s.store.TraderAdmin().GetByID(id)
	if err != nil {
		SafeNotFound(c, "Trader admin not found")
		return
	}

	// Run the scan synchronously
	runner := trader.GetTraderAdminRunner()
	result, err := runner.ScanManagedTraders(id, s.store)
	if err != nil {
		logger.Errorf("Failed to run trader admin scan: %v", err)
		SafeInternalError(c, fmt.Sprintf("Failed to run scan: %v", err), err)
		return
	}

	// Update last scan time
	s.store.TraderAdmin().UpdateLastScanTime(id)

	c.JSON(http.StatusOK, gin.H{
		"result": result,
		"admin":  toTraderAdminResponse(admin),
	})
}
