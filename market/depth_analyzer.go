package market

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// DepthWall 描述一侧订单簿上的“大单墙”
type DepthWall struct {
	Side        string  // "bid" or "ask"
	Price       float64 // 墙体价格
	Quantity    float64 // 挂单数量（合约张数/币数）
	NotionalUSD float64 // 以 midPrice 近似折算的名义价值
	DistancePct float64 // 距离 midPrice 的百分比
}

// DepthAnalysis 为 AI 提供的精简深度图特征
type DepthAnalysis struct {
	Symbol string

	MidPrice float64

	// 1% 深度内的买卖挂单名义价值（约 ±1% 区间）
	BidNotional1Pct float64
	AskNotional1Pct float64

	// 深度失衡度：(Bid - Ask) / (Bid + Ask)，>0 买盘更厚，<0 卖盘更厚
	DepthImbalance float64

	// 买卖价差（BestAsk - BestBid），用于衡量交易滑点与流动性是否枯竭
	BidAskSpread float64

	NearestBidWall *DepthWall
	NearestAskWall *DepthWall
}

// AnalyzeMarketDepth 使用 DepthManager 的内存快照进行零延迟深度分析。
// 不再发起 HTTP 轮询，而是直接读取 WS 实时流更新的 Top N 档订单簿。
// 若当前暂无快照，则返回错误；若快照较旧，会记录滞后时间但仍然使用，以避免阻塞 AI 决策。
func AnalyzeMarketDepth(symbol, exchange string) (*DepthAnalysis, error) {
	ex := strings.ToLower(strings.TrimSpace(exchange))
	if ex == "" {
		ex = "binance"
	}

	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("empty symbol")
	}

	mgr := GlobalDepthManager()
	snap, ok := mgr.GetSnapshot(sym, ex)
	if !ok || snap == nil || (len(snap.Bids) == 0 && len(snap.Asks) == 0) {
		return nil, fmt.Errorf("no depth snapshot for %s on %s", sym, ex)
	}

	bids := snap.Bids
	asks := snap.Asks
	if len(bids) == 0 || len(asks) == 0 {
		return nil, fmt.Errorf("incomplete depth snapshot for %s on %s", sym, ex)
	}

	bestBid := bids[0][0]
	bestAsk := asks[0][0]
	mid := (bestBid + bestAsk) / 2
	if mid <= 0 {
		return nil, fmt.Errorf("invalid mid price")
	}

	spread := bestAsk - bestBid

	lower := mid * 0.99
	upper := mid * 1.01

	var bidNotional, askNotional float64
	var maxBidWallNotional, maxAskWallNotional float64
	var nearestBidWall, nearestAskWall *DepthWall

	for _, b := range bids {
		price, qty := b[0], b[1]
		if price < lower {
			break
		}
		notional := price * qty
		if price <= mid {
			bidNotional += notional
		}
		distPct := (mid - price) / mid * 100
		if distPct < 0 {
			continue
		}
		if notional > maxBidWallNotional {
			maxBidWallNotional = notional
			nearestBidWall = &DepthWall{
				Side:        "bid",
				Price:       price,
				Quantity:    qty,
				NotionalUSD: notional,
				DistancePct: distPct,
			}
		}
	}

	for _, a := range asks {
		price, qty := a[0], a[1]
		if price > upper {
			break
		}
		notional := price * qty
		if price >= mid {
			askNotional += notional
		}
		distPct := (price - mid) / mid * 100
		if distPct < 0 {
			continue
		}
		if notional > maxAskWallNotional {
			maxAskWallNotional = notional
			nearestAskWall = &DepthWall{
				Side:        "ask",
				Price:       price,
				Quantity:    qty,
				NotionalUSD: notional,
				DistancePct: distPct,
			}
		}
	}

	if bidNotional <= 0 && askNotional <= 0 {
		return nil, fmt.Errorf("no depth within 1%% window")
	}

	imbalance := 0.0
	sum := bidNotional + askNotional
	if sum > 0 {
		imbalance = (bidNotional - askNotional) / sum
		// clamp to [-1,1]
		if imbalance > 1 {
			imbalance = 1
		}
		if imbalance < -1 {
			imbalance = -1
		}
	}

	// 若墙体距离过远（>5%）则认为对短线决策参考价值有限，丢弃
	const maxWallDistancePct = 5.0
	if nearestBidWall != nil && math.Abs(nearestBidWall.DistancePct) > maxWallDistancePct {
		nearestBidWall = nil
	}
	if nearestAskWall != nil && math.Abs(nearestAskWall.DistancePct) > maxWallDistancePct {
		nearestAskWall = nil
	}

	return &DepthAnalysis{
		Symbol:          sym,
		MidPrice:        mid,
		BidNotional1Pct: bidNotional,
		AskNotional1Pct: askNotional,
		DepthImbalance:  imbalance,
		BidAskSpread:    spread,
		NearestBidWall:  nearestBidWall,
		NearestAskWall:  nearestAskWall,
	}, nil
}

