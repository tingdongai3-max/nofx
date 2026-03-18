package manager

import (
	"fmt"
	"nofx/logger"
	"nofx/mcp"
	"nofx/store"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultCoachSystemPrompt = `# Role: Senior Quantitative Prompt Engineer & Trading Coach

## Objective
你现在的任务是优化一组 AI 交易员的提示词。你将看到一个“主提示词”以及它派生出的多个变体在过去 12 小时的实盘表现。

## Context Data
1. [Current Base Prompt]: 目前正在使用的核心指令。
2. [Winning Logic]: 表现优异的变体在开单时的思考片段（Reasoning）。
3. [Failure Cases]: 表现最差的变体被止损或利润大幅回吐的案例（带 MAE/MFE 数据）。

## Mutation Strategy (进化策略)
请执行以下逻辑并生成一个新的提示词变体：
1. **逻辑增强**：如果优胜者提到了某种特定形态（如“缩量回踩颈线”）而表现更好，请将此逻辑显式化。
2. **约束收紧**：分析失败案例。如果是由于在 Avg MAE 范围内止损被扫，请在提示词中加入“必须等待价格进入历史 MAE 缓冲区后再下单”的强制指令。
3. **消除幻觉**：如果 AI 在回撤期间表现出“过度自信”，请增加针对当前波动率（ATR）的风险降级机制。

## Output Format
请直接输出优化后的完整 custom_prompt。要求：
- 保持技术化风格（涉及 EMA, 2B, MAE, MFE, POC 等术语）。
- 逻辑必须闭环，不要增加废话。
- 重点放在“如何过滤高风险入场点”和“如何精准定义 Target 1/2”。`

type CoachService struct {
	store         *store.Store
	traderManager *TraderManager

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type coachVariantPerformance struct {
	Trader   *store.Trader
	Calmar   float64
	Stats    *store.TraderStats
	Source   string
	ResetAt  time.Time
	Running  bool
}

type evolutionConfig struct {
	Interval         time.Duration
	ReplacementCount int
	CoachModelID     string
}

func NewCoachService(st *store.Store, tm *TraderManager) *CoachService {
	return &CoachService{
		store:         st,
		traderManager: tm,
		stopCh:        make(chan struct{}),
	}
}

func (cs *CoachService) Start() {
	if cs.store == nil || cs.traderManager == nil {
		return
	}

	cfg := cs.loadConfig()
	logger.Infof("🧬 Coach Service started (interval=%s, replacements=%d, model=%s)", cfg.Interval, cfg.ReplacementCount, cfg.CoachModelID)

	cs.wg.Add(1)
	go func() {
		defer cs.wg.Done()
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-cs.stopCh:
				return
			case <-ticker.C:
				cfg = cs.loadConfig()
				if err := cs.RunEvolutionStep(); err != nil {
					logger.Warnf("coach evolution step failed: %v", err)
				}
				ticker.Reset(cfg.Interval)
			}
		}
	}()
}

func (cs *CoachService) Stop() {
	close(cs.stopCh)
	cs.wg.Wait()
}

func (cs *CoachService) RunEvolutionStep() error {
	experiments, err := cs.store.Experiment().ListAll()
	if err != nil {
		return err
	}
	if len(experiments) == 0 {
		return nil
	}

	cfg := cs.loadConfig()
	for _, experiment := range experiments {
		if err := cs.runExperimentEvolution(experiment, cfg); err != nil {
			logger.Warnf("coach evolution skipped for experiment %s: %v", experiment.ID, err)
		}
	}
	return nil
}

func (cs *CoachService) runExperimentEvolution(experiment *store.ExperimentRecord, cfg evolutionConfig) error {
	mutationDirections := []string{
		"Focus on strictly tightening Stop Loss based on MAE data (极限风控派)",
		"Focus on maximizing Take Profit Target 2 for trend continuation (趋势扩大派)",
		"Focus on adding strict Orderbook Imbalance filters (订单簿微观派)",
		"Focus on Contrarian entries using RSI Divergence and Fakeouts (假突破逆势派)",
		"Focus on Strict POC (Volume Point of Control) gravity pullback (成交量引力派)",
	}

	performances, err := cs.collectVariantPerformance(experiment, cfg.Interval)
	if err != nil {
		return err
	}
	if len(performances) < 2 {
		return fmt.Errorf("not enough running variants")
	}

	sort.Slice(performances, func(i, j int) bool {
		return performances[i].Calmar > performances[j].Calmar
	})

	winnerCount := minInt(3, len(performances))
	loserCount := minInt(3, len(performances))
	winners := performances[:winnerCount]
	losers := performances[len(performances)-loserCount:]

	master, err := cs.store.Trader().GetByID(experiment.MasterTraderID)
	if err != nil {
		return err
	}
	currentPrompt, err := cs.resolveCurrentPrompt(master)
	if err != nil {
		return err
	}

	coachModel, err := cs.selectCoachModel(master.UserID, cfg.CoachModelID)
	if err != nil {
		return err
	}

	winningLogic, failureCases := cs.collectEvolutionSamples(winners, losers)
	if len(winningLogic) == 0 && len(failureCases) == 0 {
		return fmt.Errorf("no usable samples in current window")
	}

	replaced := cs.selectWorstShadowVariants(performances, cfg.ReplacementCount)
	if len(replaced) == 0 {
		return fmt.Errorf("no shadow variants eligible for replacement")
	}

	resetAt := time.Now().UTC().Truncate(time.Second)
	replacedIDs := make([]string, 0, len(replaced))
	mutationSummaries := make([]string, 0, len(replaced))
	for i, perf := range replaced {
		mutationDirection := mutationDirections[i%len(mutationDirections)]
		newPrompt, err := cs.generateMutatedPrompt(coachModel, currentPrompt, winningLogic, failureCases, mutationDirection)
		if err != nil {
			return err
		}
		newPrompt = strings.TrimSpace(stripCodeFence(newPrompt))
		if newPrompt == "" {
			return fmt.Errorf("coach model returned empty prompt for trader %s", perf.Trader.ID)
		}

		if err := cs.store.Trader().UpdateCustomPrompt(perf.Trader.UserID, perf.Trader.ID, newPrompt, true); err != nil {
			return err
		}
		if err := cs.store.Trader().UpdateResetTimestamp(perf.Trader.UserID, perf.Trader.ID, resetAt); err != nil {
			return err
		}
		if memTrader, err := cs.traderManager.GetTrader(perf.Trader.ID); err == nil && memTrader != nil {
			memTrader.SetCustomPrompt(newPrompt)
			memTrader.SetOverrideBasePrompt(true)
			memTrader.ApplyDataReset(resetAt)
		}
		replacedIDs = append(replacedIDs, perf.Trader.ID)
		mutationSummaries = append(mutationSummaries, fmt.Sprintf("%s=>%s", perf.Trader.ID, mutationDirection))
		logger.Infof("🧬 Coach mutated trader %s with direction: %s", perf.Trader.ID, mutationDirection)
	}

	mutationSummaryText := strings.Join(mutationSummaries, "\n")
	logRecord := &store.ExperimentLogRecord{
		ExperimentID:      experiment.ID,
		UserID:            master.UserID,
		CoachModelID:      coachModel.ID,
		WinnerTraderIDs:   collectTraderIDs(winners),
		LoserTraderIDs:    collectTraderIDs(losers),
		ReplacedTraderIDs: replacedIDs,
		OldPrompt:         currentPrompt,
		NewPrompt:         mutationSummaryText,
		PromptDiff:        mutationSummaryText,
		Summary: fmt.Sprintf(
			"winners=%s losers=%s replaced=%s directions=%s",
			strings.Join(collectTraderIDs(winners), ","),
			strings.Join(collectTraderIDs(losers), ","),
			strings.Join(replacedIDs, ","),
			strings.Join(mutationSummaries, "; "),
		),
	}
	if err := cs.store.ExperimentLog().Create(logRecord); err != nil {
		return err
	}

	logger.Infof("🧬 Coach evolved experiment %s with model %s, replaced shadows=%s", experiment.ID, coachModel.ID, strings.Join(replacedIDs, ","))
	return nil
}

func (cs *CoachService) resolveCurrentPrompt(master *store.Trader) (string, error) {
	currentPrompt := strings.TrimSpace(master.CustomPrompt)
	if currentPrompt != "" {
		return currentPrompt, nil
	}

	records, err := cs.store.Decision().GetLatestRecords(master.ID, 1)
	if err != nil {
		return "", fmt.Errorf("load latest decision record for trader %s: %w", master.ID, err)
	}
	if len(records) == 0 {
		return "", nil
	}

	if prompt := strings.TrimSpace(records[0].InputPrompt); prompt != "" {
		return prompt, nil
	}
	if prompt := strings.TrimSpace(records[0].SystemPrompt); prompt != "" {
		return prompt, nil
	}

	return "", nil
}

func (cs *CoachService) collectVariantPerformance(experiment *store.ExperimentRecord, lookback time.Duration) ([]coachVariantPerformance, error) {
	traderIDs := append([]string{experiment.MasterTraderID}, experiment.ShadowTraderIDs...)
	performances := make([]coachVariantPerformance, 0, len(traderIDs))
	now := time.Now().UTC()

	for _, traderID := range traderIDs {
		traderCfg, err := cs.store.Trader().GetByID(traderID)
		if err != nil || traderCfg == nil {
			continue
		}

		running := traderCfg.IsRunning
		if memTrader, err := cs.traderManager.GetTrader(traderID); err == nil && memTrader != nil {
			if value, ok := memTrader.GetStatus()["is_running"].(bool); ok {
				running = value
			}
		}
		if !running {
			continue
		}

		source := ""
		if traderCfg.IsShadow {
			source = "shadow"
		} else if traderCfg.IsDryRun {
			source = "dry_run"
		}
		resetAt := traderCfg.ResetTimestamp.UTC()
		windowStart := now.Add(-lookback)
		if resetAt.After(windowStart) {
			windowStart = resetAt
		}

		stats, err := cs.store.Position().GetFullStatsBySourceSince(traderID, source, windowStart)
		if err != nil || stats == nil || stats.TotalTrades == 0 {
			continue
		}

		performances = append(performances, coachVariantPerformance{
			Trader:  traderCfg,
			Calmar:  stats.CalmarRatio,
			Stats:   stats,
			Source:  source,
			ResetAt: windowStart,
			Running: true,
		})
	}

	return performances, nil
}

func (cs *CoachService) collectEvolutionSamples(winners, losers []coachVariantPerformance) ([]string, []store.CoachFailureCase) {
	winningLogic := make([]string, 0, len(winners)*3)
	failureCases := make([]store.CoachFailureCase, 0, len(losers)*3)

	for _, perf := range winners {
		trades, err := cs.store.Position().GetRecentTradesWithReasoningBySourceSince(perf.Trader.ID, 12, perf.Source, perf.ResetAt)
		if err != nil {
			continue
		}
		for _, trade := range trades {
			if trade.RealizedPnL > 0 && strings.TrimSpace(trade.AiReasoningAtOpen) != "" {
				winningLogic = append(winningLogic, fmt.Sprintf("[%s] %s %s pnl=%.2f pnl_pct=%.2f reasoning=%s",
					perf.Trader.Name, trade.Symbol, trade.Side, trade.RealizedPnL, trade.PnLPct, trade.AiReasoningAtOpen))
			}
			if len(winningLogic) >= 18 {
				break
			}
		}
	}

	for _, perf := range losers {
		cases, err := cs.store.Position().GetWorstFailureCasesBySourceSince(perf.Trader.ID, perf.Source, perf.ResetAt, 6)
		if err != nil {
			continue
		}
		failureCases = append(failureCases, cases...)
	}

	sort.Slice(failureCases, func(i, j int) bool {
		return failureCases[i].MAEPct < failureCases[j].MAEPct
	})
	if len(failureCases) > 18 {
		failureCases = failureCases[:18]
	}

	return winningLogic, failureCases
}

func (cs *CoachService) selectWorstShadowVariants(performances []coachVariantPerformance, replacementCount int) []coachVariantPerformance {
	shadows := make([]coachVariantPerformance, 0)
	for _, perf := range performances {
		if perf.Trader.IsShadow {
			shadows = append(shadows, perf)
		}
	}
	sort.Slice(shadows, func(i, j int) bool {
		return shadows[i].Calmar < shadows[j].Calmar
	})
	if replacementCount > len(shadows) {
		replacementCount = len(shadows)
	}
	return shadows[:replacementCount]
}

func (cs *CoachService) generateMutatedPrompt(model *store.AIModel, currentPrompt string, winningLogic []string, failureCases []store.CoachFailureCase, mutationDirection string) (string, error) {
	client := newCoachAIClient(model)
	if client == nil {
		return "", fmt.Errorf("failed to create coach client for %s", model.ID)
	}

	var failureLines []string
	for _, item := range failureCases {
		failureLines = append(failureLines, fmt.Sprintf("[%s] %s %s pnl=%.2f pnl_pct=%.2f mae_pct=%.2f mfe_pct=%.2f close_reason=%s reasoning=%s",
			item.TraderID, item.Symbol, item.Side, item.RealizedPnL, item.PnLPct, item.MAEPct, item.MFEPct, item.CloseReason, item.AiReasoningAtOpen))
	}

	userPrompt := fmt.Sprintf(
		"[Current Base Prompt]\n%s\n\n[Mutation Direction]\nYou MUST forcefully apply this specific evolutionary path to the new prompt: %s\n\n[Winning Logic]\n%s\n\n[Failure Cases]\n%s",
		strings.TrimSpace(currentPrompt),
		strings.TrimSpace(mutationDirection),
		strings.Join(winningLogic, "\n"),
		strings.Join(failureLines, "\n"),
	)
	return client.CallWithMessages(defaultCoachSystemPrompt, userPrompt)
}

func (cs *CoachService) selectCoachModel(userID, configuredModelID string) (*store.AIModel, error) {
	if strings.TrimSpace(configuredModelID) != "" {
		model, err := cs.store.AIModel().Get(userID, configuredModelID)
		if err == nil && model != nil && model.Enabled && model.APIKey != "" {
			return model, nil
		}
	}

	models, err := cs.store.AIModel().List(userID)
	if err != nil {
		return nil, err
	}
	if userID != "default" {
		if defaultModels, defaultErr := cs.store.AIModel().List("default"); defaultErr == nil {
			models = append(models, defaultModels...)
		}
	}

	priority := map[string]int{
		"claude":   0,
		"openai":   1,
		"gemini":   2,
		"kimi":     3,
		"grok":     4,
		"deepseek": 5,
		"qwen":     6,
		"minimax":  7,
	}
	sort.Slice(models, func(i, j int) bool {
		pi, okI := priority[models[i].Provider]
		pj, okJ := priority[models[j].Provider]
		if !okI {
			pi = 99
		}
		if !okJ {
			pj = 99
		}
		if pi != pj {
			return pi < pj
		}
		return models[i].UpdatedAt.After(models[j].UpdatedAt)
	})

	for _, model := range models {
		if model.Enabled && model.APIKey != "" {
			return model, nil
		}
	}
	return nil, fmt.Errorf("no enabled coach model found for user %s", userID)
}

func (cs *CoachService) loadConfig() evolutionConfig {
	cfg := evolutionConfig{
		Interval:         12 * time.Hour,
		ReplacementCount: 5,
	}
	if value, err := cs.store.GetSystemConfig("coach_interval_hours"); err == nil && strings.TrimSpace(value) != "" {
		if hours, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && hours > 0 {
			cfg.Interval = time.Duration(hours) * time.Hour
		}
	}
	if value, err := cs.store.GetSystemConfig("coach_replacement_count"); err == nil && strings.TrimSpace(value) != "" {
		if count, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && count > 0 {
			cfg.ReplacementCount = count
		}
	}
	if value, err := cs.store.GetSystemConfig("coach_model_id"); err == nil {
		cfg.CoachModelID = strings.TrimSpace(value)
	}
	return cfg
}

func newCoachAIClient(model *store.AIModel) mcp.AIClient {
	if model == nil {
		return nil
	}

	var client mcp.AIClient
	switch model.Provider {
	case "qwen":
		client = mcp.NewQwenClient()
	case "deepseek":
		client = mcp.NewDeepSeekClient()
	case "claude":
		client = mcp.NewClaudeClient()
	case "kimi":
		client = mcp.NewKimiClient()
	case "gemini":
		client = mcp.NewGeminiClient()
	case "grok":
		client = mcp.NewGrokClient()
	case "openai":
		client = mcp.NewOpenAIClient()
	default:
		client = mcp.NewClient()
	}
	client.SetAPIKey(string(model.APIKey), model.CustomAPIURL, model.CustomModelName)
	client.SetTimeout(3 * time.Minute)
	return client
}

func collectTraderIDs(items []coachVariantPerformance) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Trader != nil {
			out = append(out, item.Trader.ID)
		}
	}
	return out
}

func buildPromptDiff(oldPrompt, newPrompt string) string {
	oldLines := strings.Split(strings.TrimSpace(oldPrompt), "\n")
	newLines := strings.Split(strings.TrimSpace(newPrompt), "\n")
	var b strings.Builder
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine == newLine {
			continue
		}
		if oldLine != "" {
			b.WriteString("- ")
			b.WriteString(oldLine)
			b.WriteString("\n")
		}
		if newLine != "" {
			b.WriteString("+ ")
			b.WriteString(newLine)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func stripCodeFence(input string) string {
	trimmed := strings.TrimSpace(input)
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimPrefix(trimmed, "markdown")
	trimmed = strings.TrimPrefix(trimmed, "text")
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
