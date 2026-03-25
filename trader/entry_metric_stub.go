package trader

func (at *AutoTrader) lookupCurrentExitEntryMetrics(symbol, side string) (float64, float64, bool) {
	_, _ = symbol, side
	return 0, 0, false
}
