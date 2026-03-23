package trader

import (
	"encoding/json"
	"fmt"
	"nofx/kernel"
	"regexp"
	"strconv"
	"strings"
)

var (
	failedDecisionIndexPattern = regexp.MustCompile(`decision #(\d+) validation failed`)
	decisionBlockPattern       = regexp.MustCompile(`(?s)<decision>\s*(\[[\s\S]*?\])\s*</decision>`)
	reasoningNLess30Pattern    = regexp.MustCompile(`(?i)\bN\s*[<≤]\s*30\b|\bN<30\b`)
	reasoningNSampleRange      = regexp.MustCompile(`(?i)\bN值?\s*\d+(?:\s*[-~]\s*\d+)?`)
	reasoningSampleLackPattern = regexp.MustCompile(`样本(?:量)?[^，。；,;]{0,8}不足`)
	reasoningEVNearZeroPattern = regexp.MustCompile(`(?i)EV(?:_L|_S)?\s*[≈~=]+\s*0|期望值[^，。；,;]{0,8}(?:接近|趋近|约等于|为)?\s*0`)
	reasoningPFLowPattern      = regexp.MustCompile(`(?i)PF(?:_L|_S)?\s*(?:<|<=)\s*1(?:\.2+)?|盈利因子[^，。；,;]{0,8}(?:低于|不达标|不足)`)
	reasoningOpenRejectPattern = regexp.MustCompile(`不满足(?:开仓)?条件|不适合入场|不宜开仓`)
)

type interceptedDecision struct {
	Symbol    string `json:"symbol"`
	Action    string `json:"action"`
	Reasoning string `json:"reasoning"`
}

func humanizeDecisionInterception(err error, aiDecision *kernel.FullDecision) (string, bool) {
	if err == nil {
		return "", false
	}

	errText := err.Error()
	if !strings.Contains(errText, "decision validation failed") {
		return "", false
	}

	decision, ok := extractInterceptedDecision(errText, aiDecision)
	if !ok {
		return "AI 决策理由未通过校验，决策已拦截", true
	}

	symbol := decision.Symbol
	if symbol == "" {
		symbol = "该币种"
	}

	if prompt := summarizeInterceptionPrompt(decision.Reasoning); prompt != "" {
		return fmt.Sprintf("%s：%s，决策已拦截", symbol, prompt), true
	}

	switch {
	case strings.Contains(errText, "wait reasoning must cite either Bin/Ban/区间 score, risk evidence, or statistical insufficiency"):
		return fmt.Sprintf("%s：观望理由缺少有效统计或风险证据，决策已拦截", symbol), true
	case strings.Contains(errText, "management reasoning must cite either Bin/Ban/区间 score or risk evidence"):
		return fmt.Sprintf("%s：管理理由缺少分箱或风险证据，决策已拦截", symbol), true
	case strings.Contains(errText, "reasoning must cite the referenced Bin/Ban/区间 score"):
		return fmt.Sprintf("%s：理由缺少 Bin/Ban/区间 分数引用，决策已拦截", symbol), true
	default:
		return fmt.Sprintf("%s：AI 决策理由未通过校验，决策已拦截", symbol), true
	}
}

func extractInterceptedDecision(errText string, aiDecision *kernel.FullDecision) (interceptedDecision, bool) {
	if aiDecision == nil || aiDecision.RawResponse == "" {
		return interceptedDecision{}, false
	}

	indexMatch := failedDecisionIndexPattern.FindStringSubmatch(errText)
	if len(indexMatch) != 2 {
		return interceptedDecision{}, false
	}
	index, convErr := strconv.Atoi(indexMatch[1])
	if convErr != nil || index <= 0 {
		return interceptedDecision{}, false
	}

	jsonMatch := decisionBlockPattern.FindStringSubmatch(aiDecision.RawResponse)
	if len(jsonMatch) != 2 {
		return interceptedDecision{}, false
	}

	var decisions []interceptedDecision
	if err := json.Unmarshal([]byte(jsonMatch[1]), &decisions); err != nil {
		return interceptedDecision{}, false
	}
	if index > len(decisions) {
		return interceptedDecision{}, false
	}

	return decisions[index-1], true
}

func summarizeInterceptionPrompt(reasoning string) string {
	if reasoning == "" {
		return ""
	}

	prompts := make([]string, 0, 4)

	hasSampleLack := reasoningSampleLackPattern.MatchString(reasoning)
	switch {
	case reasoningNLess30Pattern.MatchString(reasoning):
		prompts = append(prompts, "样本不足（N<30）")
	case hasSampleLack && reasoningNSampleRange.MatchString(reasoning):
		prompts = append(prompts, "样本严重不足")
	case hasSampleLack:
		prompts = append(prompts, "样本不足")
	}

	if reasoningEVNearZeroPattern.MatchString(reasoning) {
		prompts = append(prompts, "期望值接近 0")
	}
	if reasoningPFLowPattern.MatchString(reasoning) {
		prompts = append(prompts, "盈利因子低于 1.2")
	}
	if reasoningOpenRejectPattern.MatchString(reasoning) {
		prompts = append(prompts, "不满足开仓条件")
	}

	if len(prompts) == 0 {
		return ""
	}
	return strings.Join(prompts, "，")
}
