package market

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"nofx/logger"
	"sort"
	"strconv"
	"strings"
)

const (
	dexScreenerSearchURL = "https://api.dexscreener.com/latest/dex/search"
	MinDexLiquidityUSD   = 50_000.0
)

type dexSearchResponse struct {
	Pairs []dexPair `json:"pairs"`
}

type dexPair struct {
	ChainID     string `json:"chainId"`
	PairAddress string `json:"pairAddress"`
	URL         string `json:"url"`
	PriceUSD    string `json:"priceUsd"`
	BaseToken   struct {
		Address string `json:"address"`
		Name    string `json:"name"`
		Symbol  string `json:"symbol"`
	} `json:"baseToken"`
	QuoteToken struct {
		Address string `json:"address"`
		Name    string `json:"name"`
		Symbol  string `json:"symbol"`
	} `json:"quoteToken"`
	Txns map[string]struct {
		Buys  int `json:"buys"`
		Sells int `json:"sells"`
	} `json:"txns"`
	Volume    map[string]float64 `json:"volume"`
	Liquidity struct {
		USD float64 `json:"usd"`
	} `json:"liquidity"`
}

func fetchDexScreenerData(symbol string, currentPrice float64, cexVolumeH1 float64) (*DexScreenerData, error) {
	asset := strings.TrimSuffix(strings.ToUpper(symbol), "USDT")
	if asset == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	apiClient := NewAPIClient()
	req, err := http.NewRequest(http.MethodGet, dexScreenerSearchURL, nil)
	if err != nil {
		return nil, err
	}

	query := req.URL.Query()
	query.Set("q", asset)
	req.URL.RawQuery = query.Encode()

	resp, err := apiClient.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result dexSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	pairs := selectDexPairs(result.Pairs, asset, currentPrice)
	if len(pairs) == 0 {
		return nil, nil
	}

	best := pairs[0]
	volumeH1 := best.Volume["h1"]
	buys, sells := txnsForWindow(best.Txns, "h1")
	totalTxns := buys + sells
	buyRatio := 0.0
	if totalTxns > 0 {
		buyRatio = float64(buys) / float64(totalTxns)
	}

	buySellRatio := 0.0
	switch {
	case buys > 0 && sells == 0:
		buySellRatio = float64(buys)
	case sells > 0:
		buySellRatio = float64(buys) / float64(sells)
	}

	onchainToCEXRatio := 0.0
	if cexVolumeH1 > 0 {
		onchainToCEXRatio = volumeH1 / cexVolumeH1
	}

	logger.Infof("V3_AUDIT_DEX: %s, DEX_Vol_H1=%.2f, BuyRatio=%.2f", symbol, volumeH1, buyRatio)

	return &DexScreenerData{
		ChainID:           best.ChainID,
		PairAddress:       best.PairAddress,
		PairURL:           best.URL,
		LiquidityUSD:      best.Liquidity.USD,
		VolumeH1:          volumeH1,
		BuyTxnsH1:         buys,
		SellTxnsH1:        sells,
		BuyRatio:          buyRatio,
		BuySellRatio:      buySellRatio,
		CEXVolumeH1:       cexVolumeH1,
		OnchainToCEXRatio: onchainToCEXRatio,
	}, nil
}

func selectDexPairs(pairs []dexPair, asset string, currentPrice float64) []dexPair {
	filtered := make([]dexPair, 0, len(pairs))
	asset = strings.ToUpper(asset)
	for _, pair := range pairs {
		if pair.Liquidity.USD < MinDexLiquidityUSD {
			continue
		}
		if !strings.EqualFold(pair.BaseToken.Symbol, asset) {
			continue
		}
		filtered = append(filtered, pair)
	}

	return sortDexPairsByRelevance(filtered, currentPrice)
}

func sortDexPairsByRelevance(pairs []dexPair, currentPrice float64) []dexPair {
	filtered := make([]dexPair, 0, len(pairs))
	for _, pair := range pairs {
		if currentPrice > 0 && !priceCloseEnough(pair.PriceUSD, currentPrice) {
			continue
		}
		filtered = append(filtered, pair)
	}
	if len(filtered) == 0 {
		filtered = append(filtered, pairs...)
	}

	sort.Slice(filtered, func(i, j int) bool {
		iActive := dexPairActive(filtered[i])
		jActive := dexPairActive(filtered[j])
		if iActive != jActive {
			return iActive
		}
		if filtered[i].Liquidity.USD == filtered[j].Liquidity.USD {
			return filtered[i].PairAddress < filtered[j].PairAddress
		}
		return filtered[i].Liquidity.USD > filtered[j].Liquidity.USD
	})
	return filtered
}

func dexPairActive(pair dexPair) bool {
	buys, sells := txnsForWindow(pair.Txns, "h1")
	return pair.Volume["h1"] > 0 || buys+sells > 0
}

func priceCloseEnough(priceUSD string, currentPrice float64) bool {
	if currentPrice <= 0 {
		return true
	}
	dexPrice, err := strconv.ParseFloat(strings.TrimSpace(priceUSD), 64)
	if err != nil || dexPrice <= 0 {
		return true
	}
	diff := math.Abs(dexPrice-currentPrice) / currentPrice
	return diff <= 0.5
}

func txnsForWindow(txns map[string]struct {
	Buys  int `json:"buys"`
	Sells int `json:"sells"`
}, key string) (int, int) {
	if len(txns) == 0 {
		return 0, 0
	}
	if entry, ok := txns[key]; ok {
		return entry.Buys, entry.Sells
	}
	return 0, 0
}
