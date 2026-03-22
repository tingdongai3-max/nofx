package market

func latestValue(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[len(values)-1]
}

func (d *Data) LatestEMA(period int) float64 {
	if d == nil {
		return 0
	}
	return d.Indicators.EMAs[period]
}

func (d *Data) LatestRSI(period int) float64 {
	if d == nil {
		return 0
	}
	return d.Indicators.RSIs[period]
}

func (d *Data) LatestATR(period int) float64 {
	if d == nil {
		return 0
	}
	return d.Indicators.ATRs[period]
}

func (d *Data) LatestBoll(period int) BollResult {
	if d == nil {
		return BollResult{}
	}
	return d.Indicators.Bolls[period]
}

func (t *TimeframeSeriesData) LatestEMA(period int) float64 {
	if t == nil {
		return 0
	}
	return latestValue(t.Indicators.EMAs[period])
}

func (t *TimeframeSeriesData) LatestRSI(period int) float64 {
	if t == nil {
		return 0
	}
	return latestValue(t.Indicators.RSIs[period])
}

func (t *TimeframeSeriesData) LatestATR(period int) float64 {
	if t == nil {
		return 0
	}
	return t.Indicators.ATRs[period]
}

func (t *TimeframeSeriesData) LatestBoll(period int) BollResult {
	if t == nil {
		return BollResult{}
	}
	series, ok := t.Indicators.Bolls[period]
	if !ok {
		return BollResult{}
	}
	return BollResult{
		Upper:  latestValue(series.Upper),
		Middle: latestValue(series.Middle),
		Lower:  latestValue(series.Lower),
	}
}

func (t *TimeframeSeriesData) LatestMACD() float64 {
	if t == nil {
		return 0
	}
	return latestValue(t.Indicators.MACD)
}

func (t *TimeframeSeriesData) LatestDonchian(period int) DonchianResult {
	if t == nil {
		return DonchianResult{}
	}
	series, ok := t.Indicators.Donchians[period]
	if !ok {
		return DonchianResult{}
	}
	return DonchianResult{
		Upper: latestValue(series.Upper),
		Lower: latestValue(series.Lower),
		Mid:   latestValue(series.Mid),
	}
}

func (t *TimeframeSeriesData) LatestClose() float64 {
	if t == nil || len(t.Klines) == 0 {
		return 0
	}
	return t.Klines[len(t.Klines)-1].Close
}
