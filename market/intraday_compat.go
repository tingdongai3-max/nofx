package market

type intradaySeries struct {
	MidPrices []float64
	Volume    []float64
	ATR14     float64
}

// calculateIntradaySeries is kept for backward-compatible tests that validate
// the legacy "latest 10 bars + ATR14" view over a raw kline slice.
func calculateIntradaySeries(klines []Kline) *intradaySeries {
	data := &intradaySeries{
		MidPrices: make([]float64, 0, min(10, len(klines))),
		Volume:    make([]float64, 0, min(10, len(klines))),
	}
	if len(klines) == 0 {
		return data
	}

	start := len(klines) - 10
	if start < 0 {
		start = 0
	}
	for _, kline := range klines[start:] {
		data.MidPrices = append(data.MidPrices, kline.Close)
		data.Volume = append(data.Volume, kline.Volume)
	}
	data.ATR14 = calculateATR(klines, 14)
	return data
}
