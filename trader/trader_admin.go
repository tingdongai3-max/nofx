package trader

import (
	"encoding/json"
	"fmt"
	"math"
	"nofx/logger"
	"nofx/mcp"
	"nofx/store"
	"strings"
	"sync"
	"time"
)

// TraderAdminRunner manages scheduled scanning of traders
type TraderAdminRunner struct {
	schedulers map[string]*time.Timer
	mu         sync.RWMutex
}

var traderAdminRunner *TraderAdminRunner
var traderAdminRunnerOnce sync.Once

// GetTraderAdminRunner returns the singleton TraderAdminRunner
func GetTraderAdminRunner() *TraderAdminRunner {
	traderAdminRunnerOnce.Do(func() {
		traderAdminRunner = &TraderAdminRunner{
			schedulers: make(map[string]*time.Timer),
		}
	})
	return traderAdminRunner
}

// StartScheduler starts the scheduled scanning for a trader admin
func (ta *TraderAdminRunner) StartScheduler(adminID string, intervalMinutes int) error {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	// Stop existing scheduler if any
	if ta.schedulers[adminID] != nil {
		ta.schedulers[adminID].Stop()
	}

	logger.Infof("Starting trader admin scheduler for %s with interval %d minutes", adminID, intervalMinutes)

	// Run initial scan
	go func() {
		ta.runScan(adminID)
	}()

	// Schedule next scans
	duration := time.Duration(intervalMinutes) * time.Minute
	ta.schedulers[adminID] = time.AfterFunc(duration, func() {
		ta.runScan(adminID)
		// Reschedule
		ta.mu.Lock()
		if ta.schedulers[adminID] != nil {
			ta.schedulers[adminID].Stop()
		}
		ta.schedulers[adminID] = time.AfterFunc(duration, func() {
			ta.runScan(adminID)
			// Continue rescheduling
			ta.reschedule(adminID, intervalMinutes)
		})
		ta.mu.Unlock()
	})

	return nil
}

// reschedule continues the periodic scanning
func (ta *TraderAdminRunner) reschedule(adminID string, intervalMinutes int) {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	duration := time.Duration(intervalMinutes) * time.Minute
	ta.schedulers[adminID] = time.AfterFunc(duration, func() {
		ta.runScan(adminID)
		ta.reschedule(adminID, intervalMinutes)
	})
}

// StopScheduler stops the scheduled scanning for a trader admin
func (ta *TraderAdminRunner) StopScheduler(adminID string) {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	if ta.schedulers[adminID] != nil {
		ta.schedulers[adminID].Stop()
		delete(ta.schedulers, adminID)
		logger.Infof("Stopped trader admin scheduler for %s", adminID)
	}
}

// runScan runs the scan for a trader admin
func (ta *TraderAdminRunner) runScan(adminID string) {
	logger.Infof("Running trader admin scan for %s", adminID)
}

// ScanManagedTraders scans all managed traders for a trader admin
func (ta *TraderAdminRunner) ScanManagedTraders(adminID string, st *store.Store) (*AnalysisResult, error) {
	admin, err := st.TraderAdmin().GetByID(adminID)
	if err != nil {
		return nil, fmt.Errorf("failed to get trader admin: %w", err)
	}

	managedTraderIDs := admin.GetManagedTraderIDs()
	if len(managedTraderIDs) == 0 {
		return nil, fmt.Errorf("no managed traders")
	}

	result := &AnalysisResult{
		AdminID:    adminID,
		AdminName:  admin.Name,
		ScanTime:   time.Now().UTC(),
		TraderResults: make([]*TraderAnalysisResult, 0),
	}

	for _, traderID := range managedTraderIDs {
		traderResult := ta.analyzeTrader(traderID, st, admin.AIModelID)
		result.TraderResults = append(result.TraderResults, traderResult)
	}

	// Save analysis to database
	analysisData, _ := json.Marshal(result)
	for _, tr := range result.TraderResults {
		hallucinationData, _ := json.Marshal(tr.HallucinationReport)
		optimizations, _ := json.Marshal(tr.Optimizations)

		analysis := &store.TraderAdminAnalysis{
			AdminID:           adminID,
			TraderID:          tr.TraderID,
			ScanTime:          result.ScanTime,
			AnalysisData:      string(analysisData),
			HallucinationData: string(hallucinationData),
			Optimizations:     string(optimizations),
		}
		st.TraderAdminAnalysis().Create(analysis)
	}

	return result, nil
}

// analyzeTrader analyzes a single trader using AI
func (ta *TraderAdminRunner) analyzeTrader(traderID string, st *store.Store, adminModelID string) *TraderAnalysisResult {
	result := &TraderAnalysisResult{
		TraderID: traderID,
		HallucinationReport: &HallucinationReport{
			Detections: []*HallucinationDetection{},
		},
	}

	// Get trader info
	trader, err := st.Trader().GetByID(traderID)
	if err != nil {
		result.Error = fmt.Sprintf("failed to get trader: %v", err)
		return result
	}

	result.TraderName = trader.Name
	result.TraderModelID = trader.AIModelID

	// Get recent decisions (last 10)
	decisions, err := st.Decision().GetLatestRecords(traderID, 10)
	if err != nil {
		result.Error = fmt.Sprintf("failed to get decisions: %v", err)
		return result
	}

	if len(decisions) == 0 {
		result.Error = "no decisions found"
		return result
	}

	// Analyze decisions
	result.TotalDecisions = len(decisions)
	result.SuccessfulDecisions = 0
	var totalPnL float64
	var coTTraces []string

	for _, d := range decisions {
		if d.Success {
			result.SuccessfulDecisions++
		}

		// Calculate P&L from positions
		for _, pos := range d.Positions {
			totalPnL += pos.UnrealizedProfit
		}

		// Collect CoT traces
		if d.CoTTrace != "" {
			coTTraces = append(coTTraces, d.CoTTrace)
		}

		// Check hallucination for each decision (rule-based)
		hallucination := CheckHallucination(d)
		if hallucination != nil {
			result.HallucinationReport.Detections = append(result.HallucinationReport.Detections, hallucination)
		}
	}

	result.TotalPnL = totalPnL
	result.CoTTraces = coTTraces

	// Use AI to analyze the trader's reasoning and provide suggestions
	logger.Infof("🔍 Starting AI analysis for trader %s using model %s", traderID, adminModelID)
	aiAnalysis := ta.runAIAnalysis(trader, decisions, coTTraces, adminModelID, st)
	if aiAnalysis != nil {
		logger.Infof("✅ AI analysis completed for trader %s", traderID)
		// Merge AI-detected hallucinations with rule-based ones
		if aiAnalysis.HallucinationReport != nil && len(aiAnalysis.HallucinationReport.Detections) > 0 {
			result.HallucinationReport.Detections = append(result.HallucinationReport.Detections, aiAnalysis.HallucinationReport.Detections...)
		}
		// Use AI-generated optimizations
		if aiAnalysis.Optimizations != nil {
			result.Optimizations = aiAnalysis.Optimizations
		}
	} else {
		logger.Warnf("⚠️ AI analysis failed for trader %s - check AI model configuration", traderID)
	}

	// Calculate hallucination report summary
	if len(result.HallucinationReport.Detections) > 0 {
		result.HallucinationReport.HasHallucination = true
		result.HallucinationReport.TotalIssues = 0
		highCount := 0
		mediumCount := 0
		for _, det := range result.HallucinationReport.Detections {
			result.HallucinationReport.TotalIssues += len(det.Issues)
			switch det.Severity {
			case "high":
				highCount++
			case "medium":
				mediumCount++
			}
		}
		if highCount > 0 {
			result.HallucinationReport.Severity = "high"
		} else if mediumCount > 1 {
			result.HallucinationReport.Severity = "medium"
		} else {
			result.HallucinationReport.Severity = "low"
		}
	}

	// Fallback to rule-based optimizations if AI didn't provide any
	if result.Optimizations == nil {
		result.Optimizations = ta.generateOptimizations(result, trader)
	}

	return result
}

// CheckHallucination checks for hallucinations in a decision
func CheckHallucination(decision *store.DecisionRecord) *HallucinationDetection {
	detections := &HallucinationDetection{
		DecisionID:   fmt.Sprintf("%d", decision.ID),
		Timestamp:    decision.Timestamp,
		TraderID:     decision.TraderID,
		CycleNumber:  decision.CycleNumber,
		Issues:       []HallucinationIssue{},
	}

	// 1. Price validation - check if decision prices are reasonable
	for _, action := range decision.Decisions {
		if action.Price > 0 {
			// Check if price is too far from market (simple heuristic)
			// In real implementation, we'd fetch actual market price
			if action.Price < 0.0001 || action.Price > 1000000 {
				detections.Issues = append(detections.Issues, HallucinationIssue{
					Type:        "price_anomaly",
					Severity:    "high",
					Description: fmt.Sprintf("Price %f seems unrealistic for decision", action.Price),
				})
			}
		}

		// 2. Check for conflicting actions
		if strings.Contains(action.Action, "open_long") && strings.Contains(action.Action, "open_short") {
			detections.Issues = append(detections.Issues, HallucinationIssue{
				Type:        "logic_conflict",
				Severity:    "high",
				Description: "Decision contains conflicting long and short actions",
			})
		}
	}

	// 3. Check CoT trace for logical consistency
	if decision.CoTTrace != "" {
		// Simple check for contradictory statements
		coTLower := strings.ToLower(decision.CoTTrace)

		// Check for multiple contradictory signals in the same trace
		bullishCount := strings.Count(coTLower, "bullish") + strings.Count(coTLower, "long") + strings.Count(coTLower, "buy")
		bearishCount := strings.Count(coTLower, "bearish") + strings.Count(coTLower, "short") + strings.Count(coTLower, "sell")

		if bullishCount > 0 && bearishCount > 0 && math.Abs(float64(bullishCount-bearishCount)) <= 1 {
			detections.Issues = append(detections.Issues, HallucinationIssue{
				Type:        "reasoning_inconsistency",
				Severity:    "medium",
				Description: "CoT trace contains conflicting bullish and bearish signals",
			})
		}

		// 4. Check for data fabrication (mentions of non-existent indicators)
		knownIndicators := []string{"rsi", "macd", "ema", "boll", "atr", "adx", "volume", "price", "kline", "oi"}
		for _, indicator := range knownIndicators {
			if strings.Contains(coTLower, indicator) && !strings.Contains(coTLower, "no "+indicator) {
				// Indicator mentioned - this is normal, just check for fabricated values
				// Could add more sophisticated checks here
			}
		}
	}

	// 5. Check confidence vs decision consistency
	for _, action := range decision.Decisions {
		if action.Confidence > 0 {
			// If confidence is very high but reasoning is weak, flag it
			if action.Confidence >= 90 && (action.Reasoning == "" || len(action.Reasoning) < 20) {
				detections.Issues = append(detections.Issues, HallucinationIssue{
					Type:        "confidence_anomaly",
					Severity:    "low",
					Description: "High confidence without sufficient reasoning",
				})
			}
		}
	}

	// 6. Check for unrealistically profitable decisions
	for _, action := range decision.Decisions {
		if action.Price > 0 && action.Quantity > 0 {
			potentialProfit := action.Price * action.Quantity * 0.1 // Assume 10% profit target
			if potentialProfit > 1000000 { // Unrealistic profit expectation
				detections.Issues = append(detections.Issues, HallucinationIssue{
					Type:        "unrealistic_expectation",
					Severity:    "medium",
					Description: fmt.Sprintf("Unrealistic profit expectation: %f", potentialProfit),
				})
			}
		}
	}

	if len(detections.Issues) > 0 {
		detections.HasHallucination = true
		// Calculate severity
		highCount := 0
		mediumCount := 0
		for _, issue := range detections.Issues {
			switch issue.Severity {
			case "high":
				highCount++
			case "medium":
				mediumCount++
			}
		}
		if highCount > 0 {
			detections.Severity = "high"
		} else if mediumCount > 1 {
			detections.Severity = "medium"
		} else {
			detections.Severity = "low"
		}
		return detections
	}

	return nil
}

// runAIAnalysis uses AI to analyze trader's reasoning and detect issues
func (ta *TraderAdminRunner) runAIAnalysis(trader *store.Trader, decisions []*store.DecisionRecord, coTTraces []string, adminModelID string, st *store.Store) *TraderAnalysisResult {
	// Get AI model configuration
	aiModel, err := st.AIModel().GetByID(adminModelID)
	if err != nil {
		logger.Errorf("Failed to get AI model %s: %v", adminModelID, err)
		return nil
	}

	// Create AI client
	client := mcp.NewClient()

	// Configure client based on model provider
	apiKey := string(aiModel.APIKey)
	if apiKey == "" {
		logger.Errorf("AI model %s has no API key configured", adminModelID)
		return nil
	}

	switch aiModel.Provider {
	case "deepseek":
		client.SetAPIKey(apiKey, "https://api.deepseek.com", "deepseek-chat")
	case "qwen":
		client.SetAPIKey(apiKey, "https://dashscope.aliyuncs.com/compatible-mode/v1", "qwen-plus")
	case "minimax":
		// MiniMax Claude兼容API
		client.SetAPIKey(apiKey, "https://api.minimaxi.com/anthropic", "MiniMax-M2.5")
	case "custom":
		if aiModel.CustomAPIURL != "" && aiModel.CustomModelName != "" {
			client.SetAPIKey(apiKey, aiModel.CustomAPIURL, aiModel.CustomModelName)
		} else {
			logger.Errorf("Custom AI model %s missing URL or model name", adminModelID)
			return nil
		}
	default:
		logger.Errorf("Unsupported AI provider: %s", aiModel.Provider)
		return nil
	}

	// Build system prompt for the admin AI
	systemPrompt := buildAdminSystemPrompt()

	// Build user prompt with trader's data
	userPrompt := buildAdminUserPrompt(trader, decisions, coTTraces)

	// Call AI
	logger.Infof("Calling AI model %s to analyze trader %s", adminModelID, trader.ID)
	response, err := client.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		logger.Errorf("AI analysis failed: %v", err)
		return nil
	}

	// Parse AI response
	aiResult := parseAIAnalysisResponse(response)
	return aiResult
}

// buildAdminSystemPrompt creates the system prompt for the admin AI
func buildAdminSystemPrompt() string {
	return `你是一个AI交易系统管理员和质检专家。

你的职责是：
1. **检测AI幻觉**：分析交易员AI的推理过程（思维链），识别逻辑不一致、数据捏造、矛盾陈述或不切实际的假设。
2. **识别系统问题**：检查交易员的决策是否基于错误或缺失的数据、API错误或系统故障。
3. **提供策略优化建议**：改进交易员的决策流程、风险管理和整体策略。

请用中文返回优化建议。

你会收到：
- 交易员配置和当前状态
- 最近的交易决策及其推理（思维链）
- 仓位和盈亏数据

请按以下JSON格式返回：
{
  "hallucination_detections": [
    {
      "decision_id": "字符串",
      "severity": "high|medium|low",
      "issues": [
        {
          "type": "reasoning_inconsistency|data_fabrication|logic_conflict|unrealistic_expectation",
          "description": "详细说明"
        }
      ]
    }
  ],
  "system_issues": [
    {
      "type": "data_quality|api_error|missing_data|calculation_error",
      "severity": "high|medium|low",
      "description": "详细说明"
    }
  ],
  "optimizations": {
    "general": ["通用建议1", "通用建议2"],
    "specific": ["具体可执行建议1", "具体可执行建议2"]
  }
}

请详细但简洁地分析，重点关注可操作的见解。`
}

// buildAdminUserPrompt creates the user prompt with trader data
func buildAdminUserPrompt(trader *store.Trader, decisions []*store.DecisionRecord, coTTraces []string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Trader Analysis Request\n\n"))
	sb.WriteString(fmt.Sprintf("**Trader ID**: %s\n", trader.ID))
	sb.WriteString(fmt.Sprintf("**Trader Name**: %s\n", trader.Name))
	sb.WriteString(fmt.Sprintf("**AI Model**: %s\n", trader.AIModelID))
	sb.WriteString(fmt.Sprintf("**Exchange**: %s\n", trader.ExchangeID))
	sb.WriteString(fmt.Sprintf("**Total Decisions**: %d\n\n", len(decisions)))

	// Add recent decisions with CoT traces
	sb.WriteString("## Recent Trading Decisions\n\n")
	for i, decision := range decisions {
		sb.WriteString(fmt.Sprintf("### Decision #%d (Cycle %d)\n", i+1, decision.CycleNumber))
		sb.WriteString(fmt.Sprintf("- **Timestamp**: %s\n", decision.Timestamp.Format(time.RFC3339)))
		sb.WriteString(fmt.Sprintf("- **Success**: %v\n", decision.Success))

		// Add decision actions
		if len(decision.Decisions) > 0 {
			sb.WriteString("- **Actions**:\n")
			for _, action := range decision.Decisions {
				sb.WriteString(fmt.Sprintf("  - %s: %s (Confidence: %.1f%%, Price: %.4f, Qty: %.4f)\n",
					action.Symbol, action.Action, action.Confidence, action.Price, action.Quantity))
				if action.Reasoning != "" {
					sb.WriteString(fmt.Sprintf("    Reasoning: %s\n", action.Reasoning))
				}
			}
		}

		// Add positions
		if len(decision.Positions) > 0 {
			sb.WriteString("- **Positions**:\n")
			for _, pos := range decision.Positions {
				sb.WriteString(fmt.Sprintf("  - %s %s: Size=%.4f, Entry=%.4f, Current=%.4f, PnL=%.2f\n",
					pos.Symbol, pos.Side, pos.PositionAmt, pos.EntryPrice, pos.MarkPrice, pos.UnrealizedProfit))
			}
		}

		// Add Chain of Thought trace
		if decision.CoTTrace != "" {
			sb.WriteString("- **Chain of Thought (Reasoning Process)**:\n")
			sb.WriteString("```\n")
			// Truncate very long traces
			trace := decision.CoTTrace
			if len(trace) > 2000 {
				trace = trace[:2000] + "\n... (truncated)"
			}
			sb.WriteString(trace)
			sb.WriteString("\n```\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n## Analysis Request\n\n")
	sb.WriteString("Please analyze the above trading decisions and provide:\n")
	sb.WriteString("1. Any hallucinations or logical inconsistencies in the AI's reasoning\n")
	sb.WriteString("2. Potential system bugs or data quality issues\n")
	sb.WriteString("3. Optimization suggestions to improve trading performance\n\n")
	sb.WriteString("Respond in the JSON format specified in the system prompt.\n")

	return sb.String()
}

// parseAIAnalysisResponse parses the AI's JSON response
func parseAIAnalysisResponse(response string) *TraderAnalysisResult {
	result := &TraderAnalysisResult{
		HallucinationReport: &HallucinationReport{
			Detections: []*HallucinationDetection{},
		},
		Optimizations: &OptimizationSuggestions{
			General:  []string{},
			Specific: []string{},
		},
	}

	// Extract JSON from response (handle markdown code blocks)
	jsonStr := response
	if strings.Contains(response, "```json") {
		start := strings.Index(response, "```json") + 7
		end := strings.Index(response[start:], "```")
		if end > 0 {
			jsonStr = response[start : start+end]
		}
	} else if strings.Contains(response, "```") {
		start := strings.Index(response, "```") + 3
		end := strings.Index(response[start:], "```")
		if end > 0 {
			jsonStr = response[start : start+end]
		}
	}

	// Parse JSON
	var aiResponse struct {
		HallucinationDetections []struct {
			DecisionID string `json:"decision_id"`
			Severity   string `json:"severity"`
			Issues     []struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"issues"`
		} `json:"hallucination_detections"`
		SystemIssues []struct {
			Type        string `json:"type"`
			Severity    string `json:"severity"`
			Description string `json:"description"`
		} `json:"system_issues"`
		Optimizations struct {
			General  []string `json:"general"`
			Specific []string `json:"specific"`
		} `json:"optimizations"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &aiResponse); err != nil {
		logger.Errorf("Failed to parse AI response as JSON: %v", err)
		// Fallback: extract text suggestions
		result.Optimizations.General = []string{"AI analysis completed but response format was invalid"}
		return result
	}

	// Convert hallucination detections
	for _, det := range aiResponse.HallucinationDetections {
		detection := &HallucinationDetection{
			DecisionID:       det.DecisionID,
			Severity:         det.Severity,
			HasHallucination: len(det.Issues) > 0,
			Issues:           []HallucinationIssue{},
		}
		for _, issue := range det.Issues {
			detection.Issues = append(detection.Issues, HallucinationIssue{
				Type:        issue.Type,
				Severity:    det.Severity,
				Description: issue.Description,
			})
		}
		result.HallucinationReport.Detections = append(result.HallucinationReport.Detections, detection)
	}

	// Add system issues as optimizations
	for _, issue := range aiResponse.SystemIssues {
		result.Optimizations.General = append(result.Optimizations.General,
			fmt.Sprintf("[%s] %s", strings.ToUpper(issue.Severity), issue.Description))
	}

	// Add AI optimizations
	result.Optimizations.General = append(result.Optimizations.General, aiResponse.Optimizations.General...)
	result.Optimizations.Specific = append(result.Optimizations.Specific, aiResponse.Optimizations.Specific...)

	return result
}

// generateOptimizations generates optimization suggestions based on analysis (fallback)
func (ta *TraderAdminRunner) generateOptimizations(result *TraderAnalysisResult, trader *store.Trader) *OptimizationSuggestions {
	suggestions := &OptimizationSuggestions{
		TraderID: result.TraderID,
		General:  []string{},
		Specific: []string{},
	}

	// General optimizations based on success rate
	if result.TotalDecisions > 0 {
		successRate := float64(result.SuccessfulDecisions) / float64(result.TotalDecisions)
		if successRate < 0.5 {
			suggestions.General = append(suggestions.General, "Success rate is below 50%, consider reviewing decision logic")
		}
	}

	// Hallucination-based suggestions
	if result.HallucinationReport.HasHallucination {
		switch result.HallucinationReport.Severity {
		case "high":
			suggestions.General = append(suggestions.General, "High frequency of hallucinations detected, consider reducing position sizes")
			suggestions.Specific = append(suggestions.Specific, "Implement additional validation checks before executing trades")
		case "medium":
			suggestions.General = append(suggestions.General, "Moderate hallucination issues detected, review AI prompt")
		}
	}

	// P&L based suggestions
	if result.TotalPnL < 0 {
		suggestions.General = append(suggestions.General, fmt.Sprintf("Overall P&L is negative (%.2f), consider tightening stop losses", result.TotalPnL))
	}

	// CoT trace suggestions
	if len(result.CoTTraces) > 0 {
		for _, trace := range result.CoTTraces {
			if len(trace) > 2000 {
				suggestions.Specific = append(suggestions.Specific, "CoT trace is very long, consider simplifying the prompt")
				break
			}
		}
	}

	// Add default suggestion if none
	if len(suggestions.General) == 0 && len(suggestions.Specific) == 0 {
		suggestions.General = append(suggestions.General, "No major issues detected, continue monitoring")
	}

	return suggestions
}

// ============================================================================
// Data Structures
// ============================================================================

// AnalysisResult represents the overall analysis result
type AnalysisResult struct {
	AdminID       string                `json:"admin_id"`
	AdminName     string                `json:"admin_name"`
	ScanTime      time.Time             `json:"scan_time"`
	TraderResults []*TraderAnalysisResult `json:"trader_results"`
}

// TraderAnalysisResult represents analysis for a single trader
type TraderAnalysisResult struct {
	TraderID           string                   `json:"trader_id"`
	TraderName         string                   `json:"trader_name"`
	TraderModelID      string                   `json:"trader_model_id"`
	TotalDecisions    int                      `json:"total_decisions"`
	SuccessfulDecisions int                    `json:"successful_decisions"`
	TotalPnL           float64                  `json:"total_pnl"`
	CoTTraces          []string                 `json:"cot_traces"`
	HallucinationReport *HallucinationReport    `json:"hallucination_report"`
	Optimizations      *OptimizationSuggestions `json:"optimizations"`
	Error              string                   `json:"error,omitempty"`
}

// HallucinationReport represents hallucination detection results
type HallucinationReport struct {
	HasHallucination bool                      `json:"has_hallucination"`
	Severity         string                    `json:"severity"`
	TotalIssues      int                       `json:"total_issues"`
	Detections       []*HallucinationDetection `json:"detections"`
}

// HallucinationDetection represents a single hallucination detection
type HallucinationDetection struct {
	DecisionID    string             `json:"decision_id"`
	Timestamp     time.Time         `json:"timestamp"`
	TraderID      string            `json:"trader_id"`
	CycleNumber   int               `json:"cycle_number"`
	HasHallucination bool            `json:"has_hallucination"`
	Severity      string            `json:"severity"`
	Issues        []HallucinationIssue `json:"issues"`
}

// HallucinationIssue represents a specific hallucination issue
type HallucinationIssue struct {
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// OptimizationSuggestions represents optimization suggestions
type OptimizationSuggestions struct {
	TraderID string   `json:"trader_id"`
	General  []string `json:"general"`
	Specific []string `json:"specific"`
}
