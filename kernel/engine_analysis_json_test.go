package kernel

import "testing"

func TestValidateJSONFormatAllowsRangeSymbolInsideReasoning(t *testing.T) {
	jsonStr := `[{"symbol":"SIRENUSDT","action":"open_long","leverage":5,"position_size_usd":26,"stop_loss":2.13,"take_profit":2.60,"confidence":80,"risk_usd":0.65,"reasoning":"Symbol Bin 57-58: EV_L +1.3%~+2.1% 且 PF_L 1.95~2.67 显著优势。"}]`

	if err := validateJSONFormat(jsonStr); err != nil {
		t.Fatalf("expected JSON format validation to allow range symbol inside reasoning, got %v", err)
	}
}

func TestParseFullDecisionResponseAcceptsRangeSymbolInsideReasoning(t *testing.T) {
	rawResponse := `<reasoning>
## 决策逻辑
SIREN在高分段做多EV/PF指标优秀，超卖反弹概率高。
</reasoning>

<decision>
[
  {"symbol": "SIRENUSDT", "action": "open_long", "leverage": 5, "position_size_usd": 26, "stop_loss": 2.13, "take_profit": 2.60, "confidence": 80, "risk_usd": 0.65, "reasoning": "Symbol Bin 57-58: EV_L +1.3%~+2.1% 且 PF_L 1.95~2.67 显著优势；4h跌幅-12.98%超卖，空头平仓中（OI -39M），反弹概率高。"}
]
</decision>`

	decision, err := parseFullDecisionResponse(rawResponse, 26.02, 5, 5, 5.0, 1.0)
	if err != nil {
		t.Fatalf("expected range symbol inside reasoning to pass full parsing, got %v", err)
	}
	if decision == nil || len(decision.Decisions) != 1 {
		t.Fatalf("expected one parsed decision, got %+v", decision)
	}
	if decision.Decisions[0].Symbol != "SIRENUSDT" || decision.Decisions[0].Action != "open_long" {
		t.Fatalf("unexpected parsed decision: %+v", decision.Decisions[0])
	}
}
