package kernel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"nofx/logger"
	"nofx/market"
	"time"
)

const czscClientTimeout = 12 * time.Second

// czscAnalyzeRequest 请求 CZSC 中间件的 body
type czscAnalyzeRequest struct {
	Symbol   string           `json:"symbol"`
	Timeframe string          `json:"timeframe"`
	Klines   []czscKlineBar   `json:"klines"`
}

type czscKlineBar struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

// FetchCZSCLabels 调用 CZSC 中间件，对 K 线做缠论分析，返回笔/线段/中枢/买卖点标签
func FetchCZSCLabels(symbol, timeframe string, klines []market.KlineBar, serviceURL string) (*CZSCLabels, error) {
	if len(klines) < 20 {
		return nil, fmt.Errorf("czsc: need at least 20 bars, got %d", len(klines))
	}
	url := serviceURL + "/analyze"
	if serviceURL == "" {
		url = "http://127.0.0.1:8765/analyze"
	}
	bars := make([]czscKlineBar, len(klines))
	for i, k := range klines {
		bars[i] = czscKlineBar{Time: k.Time, Open: k.Open, High: k.High, Low: k.Low, Close: k.Close, Volume: k.Volume}
	}
	body, err := json.Marshal(czscAnalyzeRequest{Symbol: symbol, Timeframe: timeframe, Klines: bars})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: czscClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warnf("czsc: request failed for %s: %v", symbol, err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("czsc: status %d", resp.StatusCode)
	}
	var out CZSCLabels
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	out.Timeframe = timeframe
	return &out, nil
}
