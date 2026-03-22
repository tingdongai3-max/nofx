package market

import "strings"

func DetermineSector(symbol string, volumeH1 float64, geckoData *GeckoSentimentData) string {
	normalizedSymbol := Normalize(symbol)
	switch normalizedSymbol {
	case "BTCUSDT", "ETHUSDT":
		return "BlueChip"
	}

	if narrative := determineNarrativeSector(geckoData); narrative != "" {
		return narrative
	}

	switch {
	case volumeH1 >= 100_000_000:
		return "MajorAlt"
	case volumeH1 > 0:
		return "SmallCap"
	default:
		return "MajorAlt"
	}
}

func determineNarrativeSector(geckoData *GeckoSentimentData) string {
	if geckoData == nil {
		return ""
	}

	terms := make([]string, 0, len(geckoData.Categories)+1)
	if geckoData.CoinID != "" {
		terms = append(terms, strings.ToLower(geckoData.CoinID))
	}
	for _, category := range geckoData.Categories {
		if category == "" {
			continue
		}
		terms = append(terms, strings.ToLower(category))
	}

	if containsSectorKeyword(terms, "meme", "memes", "dog-themed", "cat-themed") {
		return "Meme"
	}
	if containsSectorKeyword(terms, "artificial intelligence", "ai", "agent", "agents") {
		return "AI"
	}

	return ""
}

func containsSectorKeyword(terms []string, keywords ...string) bool {
	for _, term := range terms {
		for _, keyword := range keywords {
			if strings.Contains(term, keyword) {
				return true
			}
		}
	}
	return false
}
