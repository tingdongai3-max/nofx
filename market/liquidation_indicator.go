package market

import (
	"context"
	"os"
	"strings"
	"time"

	"nofx/logger"
	"nofx/provider/coinank"
	"nofx/provider/coinank/coinank_enum"
)

// fetchSymbolLiquidation 使用 CoinAnk 爆仓统计接口获取最近一段时间内的多空爆仓成交额（USD）。
// 为安全起见：当 COINANK_API_KEY 未配置或请求失败时，静默返回 0，不阻塞主交易链路。
func fetchSymbolLiquidation(symbol, exchange, interval string) (longUSD, shortUSD float64) {
	apiKey := strings.TrimSpace(os.Getenv("COINANK_API_KEY"))
	if apiKey == "" {
		return 0, 0
	}

	symbol = Normalize(symbol)
	if IsXyzDexAsset(symbol) {
		return 0, 0
	}

	exchEnum := mapExchangeToEnum(exchange)
	if exchEnum == "" {
		exchEnum = coinank_enum.Binance
	}

	// 采用 1h 聚合区间，size=1 取最近一条聚合爆仓数据
	iv := coinank_enum.Hour1
	end := time.Now().UTC().UnixMilli()

	client := coinank.NewCoinankClient(coinank_enum.MainUrl, apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stats, err := client.LiquidationHistory(ctx, exchEnum, symbol, iv, end, 1)
	if err != nil || len(stats) == 0 {
		if err != nil {
			logger.Infof("⚠️  Failed to fetch liquidation stats from CoinAnk for %s %s: %v", symbol, exchange, err)
		}
		return 0, 0
	}

	last := stats[len(stats)-1]
	return last.LongTurnover, last.ShortTurnover
}

