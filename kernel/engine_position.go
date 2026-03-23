package kernel

import (
	"fmt"
	"nofx/logger"
	"regexp"
	"strings"
)

// ============================================================================
// Decision Validation
// ============================================================================

var (
	// Simple RE2 patterns only: fixed tokens plus optional whitespace and digits.
	// No nested repetition, so there is no backtracking blow-up risk here.
	reasoningBinPattern     = regexp.MustCompile(`(?i)(bin|ban|区间)\s*\d+`)
	reasoningEVLongPattern  = regexp.MustCompile(`(?i)\bEV_L\b`)
	reasoningEVShortPattern = regexp.MustCompile(`(?i)\bEV_S\b`)
	reasoningPFLongPattern  = regexp.MustCompile(`(?i)\bPF_L\b`)
	reasoningPFShortPattern = regexp.MustCompile(`(?i)\bPF_S\b`)
)

type reasoningCitationEvidence struct {
	binRef     string
	hasEVLong  bool
	hasEVShort bool
	hasPFLong  bool
	hasPFShort bool
}

func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64) error {
	for i := range decisions {
		if err := validateDecision(&decisions[i], accountEquity, btcEthLeverage, altcoinLeverage, btcEthPosRatio, altcoinPosRatio); err != nil {
			return fmt.Errorf("decision #%d validation failed: %w", i+1, err)
		}
	}
	return nil
}

func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64) error {
	validActions := map[string]bool{
		"open_long":   true,
		"open_short":  true,
		"close_long":  true,
		"close_short": true,
		"hold":        true,
		"wait":        true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("invalid action: %s", d.Action)
	}

	reasoning := strings.TrimSpace(d.Reasoning)
	if reasoning == "" {
		return fmt.Errorf("reasoning cannot be empty")
	}
	if err := validateReasoningCitation(d, reasoning); err != nil {
		return err
	}

	if d.Action == "open_long" || d.Action == "open_short" {
		maxLeverage := altcoinLeverage
		posRatio := altcoinPosRatio
		maxPositionValue := accountEquity * posRatio
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage
			posRatio = btcEthPosRatio
			maxPositionValue = accountEquity * posRatio
		}

		if d.Leverage <= 0 {
			return fmt.Errorf("leverage must be greater than 0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			logger.Infof("⚠️  [Leverage Fallback] %s leverage exceeded (%dx > %dx), auto-adjusting to limit %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("position size must be greater than 0: %.2f", d.PositionSizeUSD)
		}

		const minPositionSizeGeneral = 12.0
		const minPositionSizeBTCETH = 60.0

		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			if d.PositionSizeUSD < minPositionSizeBTCETH {
				return fmt.Errorf("%s opening amount too small (%.2f USDT), must be ≥%.2f USDT", d.Symbol, d.PositionSizeUSD, minPositionSizeBTCETH)
			}
		} else {
			if d.PositionSizeUSD < minPositionSizeGeneral {
				return fmt.Errorf("opening amount too small (%.2f USDT), must be ≥%.2f USDT", d.PositionSizeUSD, minPositionSizeGeneral)
			}
		}

		tolerance := maxPositionValue * 0.01
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("BTC/ETH single coin position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", maxPositionValue, posRatio, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("altcoin single coin position value cannot exceed %.0f USDT (%.1fx account equity), actual: %.0f", maxPositionValue, posRatio, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("stop loss and take profit must be greater than 0")
		}

		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("for long positions, stop loss price must be less than take profit price")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("for short positions, stop loss price must be greater than take profit price")
			}
		}

		var entryPrice float64
		if d.Action == "open_long" {
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2
		} else {
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		if riskRewardRatio < 3.0 {
			return fmt.Errorf("risk/reward ratio too low (%.2f:1), must be ≥3.0:1 [risk: %.2f%% reward: %.2f%%] [stop loss: %.2f take profit: %.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}

func isSystemFallbackReasoning(reasoning string) bool {
	return strings.HasPrefix(reasoning, "Model didn't output structured JSON decision")
}

func validateReasoningCitation(d *Decision, reasoning string) error {
	if isSystemFallbackReasoning(reasoning) {
		return nil
	}

	evidence := extractReasoningCitationEvidence(reasoning)
	if evidence.binRef == "" {
		return fmt.Errorf("reasoning must cite the referenced Bin/Ban/区间 score")
	}

	switch d.Action {
	case "open_long":
		if evidence.hasLongSideEvidence() {
			return nil
		}
		if evidence.hasShortSideEvidence() && !evidence.hasAnyLongMetric() {
			return fmt.Errorf("open_long reasoning must cite current-side EV_L and PF_L for %s", evidence.binRef)
		}
		logReasoningPending(d, evidence, reasoning, "open_long expects EV_L and PF_L; allowing pending-zone pass")
	case "open_short":
		if evidence.hasShortSideEvidence() {
			return nil
		}
		if evidence.hasLongSideEvidence() && !evidence.hasAnyShortMetric() {
			return fmt.Errorf("open_short reasoning must cite current-side EV_S and PF_S for %s", evidence.binRef)
		}
		logReasoningPending(d, evidence, reasoning, "open_short expects EV_S and PF_S; allowing pending-zone pass")
	default:
		if !evidence.hasAnyMetric() {
			logReasoningPending(d, evidence, reasoning, "score bin cited without EV/PF tokens; allowing pending-zone pass")
		}
	}

	return nil
}

func extractReasoningCitationEvidence(reasoning string) reasoningCitationEvidence {
	return reasoningCitationEvidence{
		binRef:     reasoningBinPattern.FindString(reasoning),
		hasEVLong:  reasoningEVLongPattern.MatchString(reasoning),
		hasEVShort: reasoningEVShortPattern.MatchString(reasoning),
		hasPFLong:  reasoningPFLongPattern.MatchString(reasoning),
		hasPFShort: reasoningPFShortPattern.MatchString(reasoning),
	}
}

func (e reasoningCitationEvidence) hasLongSideEvidence() bool {
	return e.hasEVLong && e.hasPFLong
}

func (e reasoningCitationEvidence) hasShortSideEvidence() bool {
	return e.hasEVShort && e.hasPFShort
}

func (e reasoningCitationEvidence) hasAnyLongMetric() bool {
	return e.hasEVLong || e.hasPFLong
}

func (e reasoningCitationEvidence) hasAnyShortMetric() bool {
	return e.hasEVShort || e.hasPFShort
}

func (e reasoningCitationEvidence) hasAnyMetric() bool {
	return e.hasAnyLongMetric() || e.hasAnyShortMetric()
}

func logReasoningPending(d *Decision, evidence reasoningCitationEvidence, reasoning, detail string) {
	logger.Warnf("⚠️  [Reasoning Pending] symbol=%s action=%s bin=%s detail=%s reasoning=%q",
		d.Symbol, d.Action, evidence.binRef, detail, compactReasoningForLog(reasoning))
}

func compactReasoningForLog(reasoning string) string {
	const maxLen = 220
	reasoning = strings.TrimSpace(reasoning)
	if len(reasoning) <= maxLen {
		return reasoning
	}
	return reasoning[:maxLen] + "..."
}
