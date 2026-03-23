package trader

import (
	"errors"
	"nofx/kernel"
	"strings"
	"testing"
)

func TestHumanizeDecisionInterceptionSummarizesStatisticalReasons(t *testing.T) {
	err := errors.New("failed to parse AI response: decision validation failed: decision #3 validation failed: wait reasoning must cite either Bin/Ban/区间 score, risk evidence, or statistical insufficiency")
	aiDecision := &kernel.FullDecision{
		RawResponse: `<decision>
[
  {"symbol":"BTCUSDT","action":"wait","reasoning":"Bin 50: WAIT"},
  {"symbol":"ETHUSDT","action":"wait","reasoning":"OI下降，等待。"},
  {"symbol":"AAVEUSDT","action":"wait","reasoning":"Symbol N<30样本不足，EV≈0，PF<1.2，不满足开仓条件。"}
]
</decision>`,
	}

	message, intercepted := humanizeDecisionInterception(err, aiDecision)
	if !intercepted {
		t.Fatal("expected validation failure to be classified as an interception")
	}

	expectedParts := []string{
		"AAVEUSDT",
		"样本不足（N<30）",
		"期望值接近 0",
		"盈利因子低于 1.2",
		"不满足开仓条件",
		"决策已拦截",
	}
	for _, part := range expectedParts {
		if !strings.Contains(message, part) {
			t.Fatalf("expected message to contain %q, got %q", part, message)
		}
	}
}

func TestHumanizeDecisionInterceptionMapsSevereSampleLack(t *testing.T) {
	err := errors.New("failed to parse AI response: decision validation failed: decision #3 validation failed: wait reasoning must cite either Bin/Ban/区间 score, risk evidence, or statistical insufficiency")
	aiDecision := &kernel.FullDecision{
		RawResponse: `<decision>
[
  {"symbol":"FILUSDT","action":"wait","reasoning":"Bin 50: WAIT"},
  {"symbol":"BCHUSDT","action":"wait","reasoning":"Bin 64: WAIT"},
  {"symbol":"WLFIUSDT","action":"wait","reasoning":"Symbol N值4-8样本严重不足，Trend IC -0.30反向信号，量化权重不足以覆盖风险，WAIT。"}
]
</decision>`,
	}

	message, intercepted := humanizeDecisionInterception(err, aiDecision)
	if !intercepted {
		t.Fatal("expected validation failure to be classified as an interception")
	}
	if !strings.Contains(message, "WLFIUSDT：样本严重不足，决策已拦截") {
		t.Fatalf("unexpected message: %q", message)
	}
}

func TestHumanizeDecisionInterceptionIgnoresNonValidationErrors(t *testing.T) {
	message, intercepted := humanizeDecisionInterception(errors.New("network timeout"), nil)
	if intercepted {
		t.Fatalf("expected non-validation errors to bypass interception formatting, got %q", message)
	}
	if message != "" {
		t.Fatalf("expected empty message for non-validation errors, got %q", message)
	}
}
