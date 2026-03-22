package market

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nofx/logger"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	coinGeckoBaseURL = "https://api.coingecko.com/api/v3"
	geckoCacheTTL    = 15 * time.Minute
	PrivateGeckoKey  = "CG-GcrHJEAHMCpM6UzMdVK2bwkM"
)

type geckoCacheEntry struct {
	data      *GeckoSentimentData
	expiresAt time.Time
}

type geckoIDCacheEntry struct {
	coinID    string
	expiresAt time.Time
}

var (
	geckoCacheMu sync.Mutex
	geckoCache   = map[string]geckoCacheEntry{}
	geckoIDCache = map[string]geckoIDCacheEntry{}
)

type geckoSearchResponse struct {
	Coins []struct {
		ID            string `json:"id"`
		Symbol        string `json:"symbol"`
		Name          string `json:"name"`
		MarketCapRank int    `json:"market_cap_rank"`
	} `json:"coins"`
}

type geckoCoinResponse struct {
	Categories                 []string `json:"categories"`
	SentimentVotesUpPercentage float64  `json:"sentiment_votes_up_percentage"`
	PublicInterestScore        float64  `json:"public_interest_score"`
}

func resolveGeckoCoinID(symbol string) (string, error) {
	asset := strings.TrimSuffix(strings.ToUpper(symbol), "USDT")
	asset = strings.ToLower(asset)
	if asset == "" {
		return "", fmt.Errorf("symbol is required")
	}

	now := time.Now().UTC()
	geckoCacheMu.Lock()
	if cached, ok := geckoIDCache[asset]; ok && now.Before(cached.expiresAt) && cached.coinID != "" {
		geckoCacheMu.Unlock()
		return cached.coinID, nil
	}
	geckoCacheMu.Unlock()

	body, _, _, err := geckoGET(fmt.Sprintf("%s/search?query=%s", coinGeckoBaseURL, strings.ToUpper(asset)), symbol)
	if err != nil {
		return "", err
	}

	var result geckoSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	type candidate struct {
		id   string
		rank int
	}
	candidates := make([]candidate, 0, len(result.Coins))
	for _, coin := range result.Coins {
		if !strings.EqualFold(coin.Symbol, asset) {
			continue
		}
		rank := coin.MarketCapRank
		if rank <= 0 {
			rank = 1_000_000
		}
		candidates = append(candidates, candidate{id: coin.ID, rank: rank})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("coingecko id not found for %s", symbol)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank == candidates[j].rank {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].rank < candidates[j].rank
	})

	coinID := candidates[0].id
	geckoCacheMu.Lock()
	geckoIDCache[asset] = geckoIDCacheEntry{
		coinID:    coinID,
		expiresAt: now.Add(geckoCacheTTL),
	}
	geckoCacheMu.Unlock()
	return coinID, nil
}

func fetchGeckoSentiment(coinID string) (*GeckoSentimentData, error) {
	if coinID == "" {
		return nil, fmt.Errorf("coin id is required")
	}

	now := time.Now().UTC()
	geckoCacheMu.Lock()
	if cached, ok := geckoCache[coinID]; ok && now.Before(cached.expiresAt) && cached.data != nil {
		data := *cached.data
		geckoCacheMu.Unlock()
		logger.Infof("V3_AUDIT_GECKO: %s, Interest=%.2f, Sentiment=%.2f%%", coinID, data.PublicInterestScore, data.SentimentVotesUpPercentage)
		return &data, nil
	}
	geckoCacheMu.Unlock()

	body, _, usingPrivateKey, err := geckoGET(
		fmt.Sprintf("%s/coins/%s?localization=false&tickers=false&market_data=false&community_data=true&developer_data=false&sparkline=false", coinGeckoBaseURL, coinID),
		coinID,
	)
	if err != nil {
		return nil, err
	}

	var result geckoCoinResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	data := &GeckoSentimentData{
		CoinID:                     coinID,
		Categories:                 append([]string(nil), result.Categories...),
		PublicInterestScore:        result.PublicInterestScore,
		SentimentVotesUpPercentage: result.SentimentVotesUpPercentage,
		UsingPrivateKey:            usingPrivateKey,
		CachedAt:                   now,
	}

	geckoCacheMu.Lock()
	geckoCache[coinID] = geckoCacheEntry{
		data:      data,
		expiresAt: now.Add(geckoCacheTTL),
	}
	geckoCacheMu.Unlock()

	logger.Infof("V3_AUDIT_GECKO: %s, Interest=%.2f, Sentiment=%.2f%%", coinID, data.PublicInterestScore, data.SentimentVotesUpPercentage)
	return data, nil
}

func fetchGeckoSentimentForSymbol(symbol string) (*GeckoSentimentData, error) {
	coinID, err := resolveGeckoCoinID(symbol)
	if err != nil {
		return nil, err
	}
	return fetchGeckoSentiment(coinID)
}

func geckoGET(url string, symbol string) ([]byte, int, bool, error) {
	body, status, err := geckoGETWithKey(url, symbol, true)
	if err == nil && status != http.StatusUnauthorized && status != http.StatusTooManyRequests {
		return body, status, true, nil
	}
	if status == http.StatusUnauthorized || status == http.StatusTooManyRequests {
		body, fallbackStatus, fallbackErr := geckoGETWithKey(url, symbol, false)
		return body, fallbackStatus, false, fallbackErr
	}
	return body, status, true, err
}

func geckoGETWithKey(url string, symbol string, usePrivateKey bool) ([]byte, int, error) {
	apiClient := NewAPIClient()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if usePrivateKey {
		req.Header.Set("x-cg-demo-api-key", PrivateGeckoKey)
	}

	resp, err := apiClient.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if usePrivateKey {
		logger.Infof("V3_AUDIT_GECKO_AUTH: Using private key for symbol %s, status=%d", symbol, resp.StatusCode)
	} else {
		logger.Infof("V3_AUDIT_GECKO_AUTH: Falling back to public route for symbol %s, status=%d", symbol, resp.StatusCode)
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	if resp.StatusCode >= 400 && (!usePrivateKey || (resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusTooManyRequests)) {
		return nil, resp.StatusCode, fmt.Errorf("coingecko request failed: %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}
