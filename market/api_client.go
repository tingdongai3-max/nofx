package market

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"nofx/hook"
	"nofx/binanceguard"
	"strconv"
	"time"
)

const defaultMarketBaseURL = "https://fapi.binance.com"

type APIClient struct {
	client  *http.Client
	baseURL string
}

// NewAPIClient creates a new API client with default configuration.
// The underlying *http.Client can be overridden by hooks (e.g. CoinAnk, proxy, etc.)
// so that all market data requests automatically respect user's data provider settings.
func NewAPIClient() *APIClient {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	hookRes := hook.HookExec[hook.SetHttpClientResult](hook.SET_HTTP_CLIENT, client)
	if hookRes != nil && hookRes.Error() == nil {
		log.Printf("Using HTTP client set by Hook")
		client = hookRes.GetResult()
	}

	return &APIClient{
		client:  client,
		baseURL: defaultMarketBaseURL,
	}
}

// GetBaseURL returns the current market data base URL (e.g. Binance or CoinAnk proxy).
// Used by GetKlinesRange and other callers so requests can go through configured provider.
func (c *APIClient) GetBaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return defaultMarketBaseURL
}

// GetAPIClient is a convenience alias that returns a new APIClient.
// Prefer using this in other packages when fetching market data so that
// all K-line requests go through the unified, hook-aware HTTP client.
func GetAPIClient() *APIClient {
	return NewAPIClient()
}

func translateBinanceRESTError(resp *http.Response, body []byte) error {
	if resp == nil {
		return nil
	}
	if resp.StatusCode < http.StatusBadRequest && !strings.Contains(string(body), "\"code\":-1003") {
		return nil
	}
	err := fmt.Errorf("binance REST %s: %s", resp.Status, strings.TrimSpace(string(body)))
	binanceguard.SetCircuitBreakerFromError(err)
	return err
}

func (c *APIClient) GetExchangeInfo() (*ExchangeInfo, error) {
	url := fmt.Sprintf("%s/fapi/v1/exchangeInfo", c.GetBaseURL())
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var exchangeInfo ExchangeInfo
	err = json.Unmarshal(body, &exchangeInfo)
	if err != nil {
		return nil, err
	}

	return &exchangeInfo, nil
}

func (c *APIClient) GetKlines(symbol, interval string, limit int) ([]Kline, error) {
	if err := binanceguard.CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/fapi/v1/klines", c.GetBaseURL())
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("symbol", symbol)
	q.Add("interval", interval)
	q.Add("limit", strconv.Itoa(limit))
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if restErr := translateBinanceRESTError(resp, body); restErr != nil {
		return nil, restErr
	}

	var klineResponses []KlineResponse
	err = json.Unmarshal(body, &klineResponses)
	if err != nil {
		log.Printf("Failed to get K-line data, response content: %s", string(body))
		return nil, err
	}

	var klines []Kline
	for _, kr := range klineResponses {
		kline, err := parseKline(kr)
		if err != nil {
			log.Printf("Failed to parse K-line data: %v", err)
			continue
		}
		klines = append(klines, kline)
	}

	return klines, nil
}

func parseKline(kr KlineResponse) (Kline, error) {
	var kline Kline

	if len(kr) < 11 {
		return kline, fmt.Errorf("invalid kline data")
	}

	// Parse each field
	kline.OpenTime = int64(kr[0].(float64))
	kline.Open, _ = strconv.ParseFloat(kr[1].(string), 64)
	kline.High, _ = strconv.ParseFloat(kr[2].(string), 64)
	kline.Low, _ = strconv.ParseFloat(kr[3].(string), 64)
	kline.Close, _ = strconv.ParseFloat(kr[4].(string), 64)
	kline.Volume, _ = strconv.ParseFloat(kr[5].(string), 64)
	kline.CloseTime = int64(kr[6].(float64))
	kline.QuoteVolume, _ = strconv.ParseFloat(kr[7].(string), 64)
	kline.Trades = int(kr[8].(float64))
	kline.TakerBuyBaseVolume, _ = strconv.ParseFloat(kr[9].(string), 64)
	kline.TakerBuyQuoteVolume, _ = strconv.ParseFloat(kr[10].(string), 64)

	return kline, nil
}

func (c *APIClient) GetCurrentPrice(symbol string) (float64, error) {
	if err := binanceguard.CheckCircuitBreaker(); err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/fapi/v1/ticker/price", c.GetBaseURL())
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}

	q := req.URL.Query()
	q.Add("symbol", symbol)
	req.URL.RawQuery = q.Encode()

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if restErr := translateBinanceRESTError(resp, body); restErr != nil {
		return 0, restErr
	}

	var ticker PriceTicker
	err = json.Unmarshal(body, &ticker)
	if err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}
