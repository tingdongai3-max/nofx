package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	sysconfig "nofx/config"
	"nofx/logger"
	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
)

const (
	// binanceMaxKlineLimit mirrors the default limit used by Binance futures K-line API.
	// For CoinAnk we usually don't need pagination; we compute a safe upper bound on bars.
	binanceMaxKlineLimit = 1500
)

// GetKlinesRange fetches K-line series within specified time range (closed interval), returns data sorted by time in ascending order.
// When MARKET_SOURCE=coinank (default), it will use CoinAnk open API to avoid Binance IP restrictions.
func GetKlinesRange(symbol string, timeframe string, start, end time.Time) ([]Kline, error) {
	symbol = Normalize(symbol)
	normTF, err := NormalizeTimeframe(timeframe)
	if err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, fmt.Errorf("end time must be after start time")
	}

	cfg := sysconfig.Get()
	source := cfg.MarketSource
	if source == "" {
		source = "coinank"
	}

	// Prefer CoinAnk for historical klines to bypass Binance IP/451 issues.
	if source == "coinank" {
		klines, err := getKlinesRangeFromCoinAnk(symbol, normTF, start, end)
		if err != nil {
			logger.Infof("⚠️ CoinAnk GetKlinesRange failed for %s %s: %v", symbol, normTF, err)
		}
		return klines, err
	}

	// Fallback: Binance via unified API client (requires proper proxy when in restricted regions).
	// We keep this path for advanced users who explicitly set MARKET_SOURCE=binance.
	return getKlinesRangeFromBinance(symbol, normTF, start, end)
}

func getKlinesRangeFromCoinAnk(symbol, timeframe string, start, end time.Time) ([]Kline, error) {
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	if !end.After(start) {
		return nil, fmt.Errorf("end time must be after start time")
	}

	tfDur, err := TFDuration(timeframe)
	if err != nil {
		return nil, err
	}
	tfMs := int64(tfDur / time.Millisecond)
	if tfMs <= 0 {
		return nil, fmt.Errorf("invalid timeframe duration: %s", timeframe)
	}

	// Estimate number of bars needed to cover [start, end], plus buffer.
	approxBars := int((endMs-startMs)/tfMs) + 50
	if approxBars < 100 {
		approxBars = 100
	}
	if approxBars > binanceMaxKlineLimit {
		approxBars = binanceMaxKlineLimit
	}

	// Map timeframe to CoinAnk interval enum.
	var coinankInterval coinank_enum.Interval
	switch timeframe {
	case "1m":
		coinankInterval = coinank_enum.Minute1
	case "3m":
		coinankInterval = coinank_enum.Minute3
	case "5m":
		coinankInterval = coinank_enum.Minute5
	case "15m":
		coinankInterval = coinank_enum.Minute15
	case "30m":
		coinankInterval = coinank_enum.Minute30
	case "1h":
		coinankInterval = coinank_enum.Hour1
	case "2h":
		coinankInterval = coinank_enum.Hour2
	case "4h":
		coinankInterval = coinank_enum.Hour4
	case "6h":
		coinankInterval = coinank_enum.Hour6
	case "8h":
		coinankInterval = coinank_enum.Hour8
	case "12h":
		coinankInterval = coinank_enum.Hour12
	case "1d":
		coinankInterval = coinank_enum.Day1
	case "3d":
		coinankInterval = coinank_enum.Day3
	case "1w":
		coinankInterval = coinank_enum.Week1
	default:
		return nil, fmt.Errorf("unsupported timeframe for CoinAnk: %s", timeframe)
	}

	ctx := context.Background()
	ts := endMs

	// Use Binance as exchange inside CoinAnk; CoinAnk will handle routing/aggregation.
	coinankKlines, err := coinank_api.Kline(ctx, symbol, coinank_enum.Binance, ts, coinank_enum.To, approxBars, coinankInterval)
	if err != nil {
		return nil, fmt.Errorf("CoinAnk Kline error: %w", err)
	}
	if len(coinankKlines) == 0 {
		return nil, fmt.Errorf("no klines from CoinAnk for %s %s", symbol, timeframe)
	}

	klines := make([]Kline, 0, len(coinankKlines))
	for _, ck := range coinankKlines {
		if ck.StartTime < startMs-tfMs || ck.StartTime > endMs+tfMs {
			continue
		}
		klines = append(klines, Kline{
			OpenTime:  ck.StartTime,
			Open:      ck.Open,
			High:      ck.High,
			Low:       ck.Low,
			Close:     ck.Close,
			Volume:    ck.Volume,
			CloseTime: ck.EndTime,
		})
	}
	if len(klines) == 0 {
		return nil, fmt.Errorf("no klines within range for %s %s", symbol, timeframe)
	}
	return klines, nil
}

func getKlinesRangeFromBinance(symbol, timeframe string, start, end time.Time) ([]Kline, error) {
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()

	var all []Kline
	cursor := startMs

	apiClient := GetAPIClient()
	httpClient := apiClient.client

	for cursor < endMs {
		url := fmt.Sprintf("%s/fapi/v1/klines", apiClient.GetBaseURL())
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		q := req.URL.Query()
		q.Set("symbol", symbol)
		q.Set("interval", timeframe)
		q.Set("limit", fmt.Sprintf("%d", binanceMaxKlineLimit))
		q.Set("startTime", fmt.Sprintf("%d", cursor))
		q.Set("endTime", fmt.Sprintf("%d", endMs))
		req.URL.RawQuery = q.Encode()

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance klines api returned status %d: %s", resp.StatusCode, string(body))
		}

		var raw []KlineResponse
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}

		batch := make([]Kline, 0, len(raw))
		for _, item := range raw {
			k, err := parseKline(item)
			if err != nil {
				continue
			}
			batch = append(batch, k)
		}
		if len(batch) == 0 {
			break
		}

		all = append(all, batch...)
		last := batch[len(batch)-1]
		cursor = last.CloseTime + 1

		if len(batch) < binanceMaxKlineLimit {
			break
		}
	}
	return all, nil
}
