package manager

import (
	"encoding/xml"
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
你现在的任务是优化一组 AI 交易员的提示词。你将看到一个主策略灵魂、当前运行中的 Custom Prompt、它派生出的多个变体在最近窗口的实盘/影子表现，以及近期进化历史。

## Context Data
1. [Strategy Soul]: 策略必须保留的核心思想。
2. [Current Running Prompt]: 当前这个变体正在使用的完整 Custom Prompt。
3. [Variant Performance]: 当前变体的绩效指标（Calmar、收益率、回撤、交易数）。
4. [Winning Logic]: 表现优异的变体在开单时的思考片段（Reasoning）。
5. [Failure Cases]: 表现最差的变体被止损或利润大幅回吐的案例（带 MAE/MFE 数据）。
6. [Evolution Logs]: 最近几次对这个实验做过的改动记录。

## Agentic Diff Policy
你必须像一个给交易策略提 PR 的程序员一样工作，而不是像随机改写器：
1. 先评估当前提示词是否“基本正确”。
2. 如果当前逻辑主框架仍有效，只针对亏损样本暴露出的错误做 **Minimum Code Change**，例如：
   - 止损过窄
   - 追高/追空点位不对
   - Target 1/2 定义不清
   - 订单簿/波动率过滤不足
3. 只有当当前变体 **Calmar < 0 且回撤失控** 时，才允许执行 **Full Refactor**。
4. 无论如何修改，都必须保留策略灵魂：**支撑位扫荡 / liquidity sweep / support sweep** 相关核心逻辑，严禁在进化过程中丢失。
5. 修改必须保持逻辑连续性，优先修补 bug、调参数、收紧约束，而不是换一套完全无关的方法论。

## Output Format
严格按照以下 XML 输出，不要输出其他内容：
<coach_update>
  <mode>minimum_change|full_refactor</mode>
  <reasoning>说明你为什么这么改、改了哪些点、预期影响是什么。</reasoning>
  <new_prompt>输出优化后的完整 custom_prompt。</new_prompt>
</coach_update>

要求：
- 保持技术化风格（涉及 EMA, 2B, MAE, MFE, POC, sweep 等术语）。
- 逻辑必须闭环，不要增加废话。
- 重点放在“如何过滤高风险入场点”和“如何精准定义 Target 1/2”。
- Minimum Code Change 时尽量保留原文结构，只改必要片段。`

const (
	coachEvolutionLogWindow      = 8
	coachEvolutionLogTokenBudget = 160000
	shadowGenerationEquity       = 10000.0
)

const strategySoulPrompt = "Preserve the strategy soul: support-sweep / liquidity-sweep entries around key support or support-resistance zones, with disciplined confirmation before entry."

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

type coachMutationResult struct {
	Mode       string
	Reasoning  string
	NewPrompt  string
	PromptDiff string
}

type coachUpdateXML struct {
	XMLName   xml.Name `xml:"coach_update"`
	Mode      string   `xml:"mode"`
	Reasoning string   `xml:"reasoning"`
	NewPrompt string   `xml:"new_prompt"`
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
	basePrompt, err := cs.resolveCurrentPrompt(master)
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

	evolutionLogs, err := cs.store.ExperimentLog().ListRecentByExperiment(experiment.ID, coachEvolutionLogWindow)
	if err != nil {
		return err
	}
	historyWindow := buildCoachHistoryWindow(evolutionLogs, coachEvolutionLogTokenBudget, coachEvolutionLogWindow)

	resetAt := time.Now().UTC().Truncate(time.Second)
	replacedIDs := make([]string, 0, len(replaced))
	for i, perf := range replaced {
		mutationDirection := mutationDirections[i%len(mutationDirections)]
		currentVariantPrompt, err := cs.resolveCurrentPrompt(perf.Trader)
		if err != nil {
			return err
		}
		if strings.TrimSpace(currentVariantPrompt) == "" {
			currentVariantPrompt = strings.TrimSpace(basePrompt)
		}

		mutationResult, err := cs.generateMutatedPrompt(coachModel, currentVariantPrompt, winningLogic, failureCases, mutationDirection, perf, historyWindow)
		if err != nil {
			return err
		}
		if mutationResult.NewPrompt == "" {
			return fmt.Errorf("coach model returned empty prompt for trader %s", perf.Trader.ID)
		}

		if err := cs.store.Trader().UpdateCustomPrompt(perf.Trader.UserID, perf.Trader.ID, mutationResult.NewPrompt, true); err != nil {
			return err
		}
		if err := cs.store.Trader().ResetVirtualGeneration(perf.Trader.UserID, perf.Trader.ID, shadowGenerationEquity, shadowGenerationEquity); err != nil {
			return err
		}
		if err := cs.store.Trader().UpdateResetTimestamp(perf.Trader.UserID, perf.Trader.ID, resetAt); err != nil {
			return err
		}
		if err := cs.store.Position().DeleteOpenPositionsBySource(perf.Trader.ID, "shadow"); err != nil {
			return err
		}
		if memTrader, err := cs.traderManager.GetTrader(perf.Trader.ID); err == nil && memTrader != nil {
			memTrader.SetCustomPrompt(mutationResult.NewPrompt)
			memTrader.SetOverrideBasePrompt(true)
			memTrader.SetVirtualEquity(shadowGenerationEquity)
			memTrader.SetInitialBalance(shadowGenerationEquity)
			memTrader.ApplyDataReset(resetAt)
		}
		go func(traderID string) {
			if err := cs.store.CleanupTraderVariantDataBefore(traderID, resetAt); err != nil {
				logger.Warnf("failed to cleanup stale coach variant data for trader %s: %v", traderID, err)
			}
		}(perf.Trader.ID)
		replacedIDs = append(replacedIDs, perf.Trader.ID)

		logRecord := &store.ExperimentLogRecord{
			ExperimentID:      experiment.ID,
			UserID:            master.UserID,
			CoachModelID:      coachModel.ID,
			CoachReasoning:    mutationResult.Reasoning,
			WinnerTraderIDs:   collectTraderIDs(winners),
			LoserTraderIDs:    collectTraderIDs(losers),
			ReplacedTraderIDs: []string{perf.Trader.ID},
			OldPrompt:         currentVariantPrompt,
			NewPrompt:         mutationResult.NewPrompt,
			PromptDiff:        mutationResult.PromptDiff,
			Summary: fmt.Sprintf(
				"trader=%s mode=%s direction=%s calmar=%.4f total_pnl=%.2f drawdown_pct=%.2f",
				perf.Trader.ID,
				mutationResult.Mode,
				mutationDirection,
				perf.Calmar,
				safeCoachStatValue(perf.Stats.TotalPnL),
				safeCoachStatValue(perf.Stats.MaxDrawdownPct),
			),
		}
		if err := cs.store.ExperimentLog().Create(logRecord); err != nil {
			return err
		}
		logger.Infof("🧬 Coach mutated trader %s with direction: %s", perf.Trader.ID, mutationDirection)
	}

	logger.Infof("🧬 Coach evolved experiment %s with model %s, replaced shadows=%s", experiment.ID, coachModel.ID, strings.Join(replacedIDs, ","))
	return nil
}

func (cs *CoachService) resolveCurrentPrompt(master *store.Trader) (string, error) {
	currentPrompt := strings.TrimSpace(master.CustomPrompt)
	if currentPrompt != "" {
		return currentPrompt, nil
	}

	if prompt := strings.TrimSpace(master.SystemPromptTemplate); prompt != "" {
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

func (cs *CoachService) generateMutatedPrompt(model *store.AIModel, currentPrompt string, winningLogic []string, failureCases []store.CoachFailureCase, mutationDirection string, perf coachVariantPerformance, evolutionLogs string) (*coachMutationResult, error) {
	client := newCoachAIClient(model)
	if client == nil {
		return nil, fmt.Errorf("failed to create coach client for %s", model.ID)
	}

	var failureLines []string
	for _, item := range failureCases {
		failureLines = append(failureLines, fmt.Sprintf("[%s] %s %s pnl=%.2f pnl_pct=%.2f mae_pct=%.2f mfe_pct=%.2f close_reason=%s reasoning=%s",
			item.TraderID, item.Symbol, item.Side, item.RealizedPnL, item.PnLPct, item.MAEPct, item.MFEPct, item.CloseReason, item.AiReasoningAtOpen))
	}

	userPrompt := fmt.Sprintf(
		"[Strategy Soul]\n%s\n\n[Current Running Prompt]\n%s\n\n[Variant Performance]\ntrader_id=%s\ncalmar=%.4f\ntotal_pnl=%.2f\ndrawdown_pct=%.2f\ntotal_trades=%d\n\n[Recommended Update Mode]\n%s\n\n[Mutation Direction]\nYou MUST apply this evolutionary path, but follow the Agentic Diff Policy when deciding whether to make a minimum change or a full refactor: %s\n\n[Evolution Logs]\n%s\n\n[Winning Logic]\n%s\n\n[Failure Cases]\n%s",
		strategySoulPrompt,
		strings.TrimSpace(currentPrompt),
		perf.Trader.ID,
		perf.Calmar,
		safeCoachStatValue(perf.Stats.TotalPnL),
		safeCoachStatValue(perf.Stats.MaxDrawdownPct),
		safeCoachTradeCount(perf.Stats),
		coachRecommendedMode(perf),
		strings.TrimSpace(mutationDirection),
		strings.TrimSpace(evolutionLogs),
		strings.Join(winningLogic, "\n"),
		strings.Join(failureLines, "\n"),
	)
	raw, err := client.CallWithMessages(defaultCoachSystemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}
	return parseCoachMutationResult(raw, currentPrompt)
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

func parseCoachMutationResult(raw, currentPrompt string) (*coachMutationResult, error) {
	payload := strings.TrimSpace(stripCodeFence(raw))
	if payload == "" {
		return nil, fmt.Errorf("empty coach response")
	}

	start := strings.Index(payload, "<coach_update>")
	end := strings.LastIndex(payload, "</coach_update>")
	if start >= 0 && end >= 0 {
		payload = payload[start : end+len("</coach_update>")]
	}

	var parsed coachUpdateXML
	if err := xml.Unmarshal([]byte(payload), &parsed); err != nil {
		parsed.NewPrompt = extractCoachTag(payload, "new_prompt")
		parsed.Reasoning = extractCoachTag(payload, "reasoning")
		parsed.Mode = extractCoachTag(payload, "mode")
	}

	newPrompt := strings.TrimSpace(stripCodeFence(parsed.NewPrompt))
	if newPrompt == "" {
		return nil, fmt.Errorf("coach response missing new_prompt")
	}
	mode := strings.ToLower(strings.TrimSpace(parsed.Mode))
	if mode == "" {
		mode = "minimum_change"
	}

	return &coachMutationResult{
		Mode:       mode,
		Reasoning:  strings.TrimSpace(parsed.Reasoning),
		NewPrompt:  newPrompt,
		PromptDiff: buildPromptDiff(currentPrompt, newPrompt),
	}, nil
}

func extractCoachTag(payload, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	start := strings.Index(payload, openTag)
	end := strings.Index(payload, closeTag)
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return payload[start+len(openTag) : end]
}

func buildCoachHistoryWindow(logs []*store.ExperimentLogRecord, maxApproxTokens int, maxItems int) string {
	if len(logs) == 0 {
		return "(none)"
	}

	if maxItems > 0 && len(logs) > maxItems {
		logs = logs[:maxItems]
	}

	sections := make([]string, 0, len(logs))
	totalApproxTokens := 0
	for _, log := range logs {
		section := fmt.Sprintf(
			"[log %d]\ncreated_at=%s\nreplaced=%s\nsummary=%s\nreasoning=%s\ndiff=%s",
			log.ID,
			log.CreatedAt.UTC().Format(time.RFC3339),
			strings.Join(log.ReplacedTraderIDs, ","),
			strings.TrimSpace(log.Summary),
			strings.TrimSpace(log.CoachReasoning),
			strings.TrimSpace(log.PromptDiff),
		)
		approxTokens := estimatePromptTokens(section)
		if len(sections) > 0 && totalApproxTokens+approxTokens > maxApproxTokens {
			break
		}
		sections = append(sections, section)
		totalApproxTokens += approxTokens
	}

	if len(sections) == 0 {
		return "(trimmed due to token budget)"
	}
	return strings.Join(sections, "\n\n")
}

func estimatePromptTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return (len([]rune(text)) / 4) + 1
}

func safeCoachStatValue(v float64) float64 {
	if v != v {
		return 0
	}
	return v
}

func safeCoachTradeCount(stats *store.TraderStats) int {
	if stats == nil {
		return 0
	}
	return stats.TotalTrades
}

func coachRecommendedMode(perf coachVariantPerformance) string {
	if perf.Calmar < 0 && safeCoachStatValue(perf.Stats.MaxDrawdownPct) >= 15 {
		return "full_refactor_allowed: calmar is negative and drawdown is uncontrolled"
	}
	return "minimum_change_only: strategy skeleton is assumed salvageable, preserve the core sweep logic"
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
