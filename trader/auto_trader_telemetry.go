package trader

import (
	"math"
	"nofx/logger"
	"nofx/store"
	"strings"
	"time"
)

func (at *AutoTrader) recordPositionTelemetry(symbol, side string, price float64) {
	if at.store == nil || price <= 0 {
		return
	}

	dbPos, err := at.lookupStoredOpenPosition(symbol, side)
	if err != nil {
		logger.Warnf("⚠️ Failed to resolve stored position for telemetry: %s %s: %v", symbol, side, err)
		return
	}
	if dbPos == nil {
		return
	}

	heat, tradingSub, quantSub := at.lookupTelemetryScores(symbol, dbPos.Telemetry)
	point := store.FactorTelemetry{
		Timestamp:  time.Now().UTC().UnixMilli(),
		Price:      price,
		HeatScore:  heat,
		TradingSub: tradingSub,
		QuantSub:   quantSub,
	}

	if err := at.store.Position().AppendTelemetryPoint(dbPos.ID, point); err != nil {
		logger.Warnf("⚠️ Failed to append telemetry: position=%d symbol=%s side=%s err=%v", dbPos.ID, symbol, side, err)
		return
	}

	logger.Debugf("V3_AUDIT_TELEMETRY: Position=%s, Added Point=[Price=%.4f, Heat=%.1f]", dbPos.Symbol, price, heat)
	logger.Infof("V3_AUDIT_TELEMETRY: Position=%s, Added Point=[Price=%.4f, Heat=%.1f]", dbPos.Symbol, price, heat)
}

func (at *AutoTrader) mergePositionTelemetry(symbol, side string, payload map[string]interface{}) {
	if payload == nil {
		return
	}

	payload["telemetry"] = []store.FactorTelemetry{}
	if at.store == nil {
		return
	}

	dbPos, err := at.lookupStoredOpenPosition(symbol, side)
	if err != nil {
		logger.Warnf("⚠️ Failed to load telemetry for %s %s: %v", symbol, side, err)
		return
	}
	if dbPos == nil {
		return
	}

	payload["telemetry"] = []store.FactorTelemetry(dbPos.Telemetry)
}

func (at *AutoTrader) lookupStoredOpenPosition(symbol, side string) (*store.TraderPosition, error) {
	if at.store == nil {
		return nil, nil
	}

	candidates := []string{side, strings.ToUpper(side), strings.ToLower(side)}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		pos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, candidate)
		if err != nil {
			return nil, err
		}
		if pos != nil {
			return pos, nil
		}
	}

	return nil, nil
}

func (at *AutoTrader) lookupTelemetryScores(symbol string, fallback store.FactorTelemetrySeries) (float64, float64, float64) {
	at.candidateSnapshotMu.RLock()
	defer at.candidateSnapshotMu.RUnlock()

	normalized := strings.ToUpper(symbol)
	if at.candidateTelemetry != nil {
		if candidate, ok := at.candidateTelemetry[normalized]; ok {
			return sanitizeTelemetryScore(candidate.Heat),
				sanitizeTelemetryScore(candidate.TradingSub),
				sanitizeTelemetryScore(candidate.QuantSub)
		}
	}

	if len(fallback) == 0 {
		return 0, 0, 0
	}

	last := fallback[len(fallback)-1]
	return sanitizeTelemetryScore(last.HeatScore),
		sanitizeTelemetryScore(last.TradingSub),
		sanitizeTelemetryScore(last.QuantSub)
}

func sanitizeTelemetryScore(v float64) float64 {
	switch {
	case math.IsNaN(v), math.IsInf(v, 0):
		return 0
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}
