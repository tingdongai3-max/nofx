package market

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
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
		// 本地快照完全缺失时，尝试使用一次性 HTTP REST 做兜底（避免因 WS 初始握手失败而完全没有深度信息）。
		if httpSnap, err := fetchDepthSnapshotHTTP(sym, ex); err == nil {
			snap = httpSnap
		} else {
			return nil, fmt.Errorf("no depth snapshot for %s on %s (WS empty, HTTP fallback failed: %v)", sym, ex, err)
		}
	}

	bids := snap.Bids
	asks := snap.Asks
	if len(bids) == 0 || len(asks) == 0 {
		return nil, fmt.Errorf("incomplete depth snapshot for %s on %s", sym, ex)
	}

	// Stale 判定：若最后更新时间早于当前超过 10 秒，说明 WS 更新可能滞后/短暂断开。
	// 此时尝试额外发起一次 HTTP 请求作为补丁；若失败，则继续使用当前快照，不阻塞 AI 决策。
	const staleThreshold = 10 * time.Second
	if !snap.UpdatedAt.IsZero() {
		age := time.Since(snap.UpdatedAt)
		if age > staleThreshold {
			if httpSnap, err := fetchDepthSnapshotHTTP(sym, ex); err == nil && httpSnap != nil &&
				len(httpSnap.Bids) > 0 && len(httpSnap.Asks) > 0 {
				snap = httpSnap
				bids = snap.Bids
				asks = snap.Asks
			}
		}
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

// fetchDepthSnapshotHTTP 使用 HTTP REST 获取一次性订单簿快照，用于 WS 长时间滞后的兜底方案。
// 为了控制复杂度，目前仅对 binance 做实现；其它交易所返回错误，由调用方决定是否使用旧快照继续决策。
func fetchDepthSnapshotHTTP(symbol, exchange string) (*OrderBookSnapshot, error) {
	ex := strings.ToLower(strings.TrimSpace(exchange))
	sym := strings.ToUpper(strings.TrimSpace(symbol))

	switch ex {
	case "binance":
		const depthLimit = 20
		url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/depth?symbol=%s&limit=%d", sym, depthLimit)

		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance depth http status=%d", resp.StatusCode)
		}

		var payload struct {
			LastUpdateID int         `json:"lastUpdateId"`
			Bids         [][]string  `json:"bids"`
			Asks         [][]string  `json:"asks"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if len(payload.Bids) == 0 || len(payload.Asks) == 0 {
			return nil, fmt.Errorf("empty binance depth for %s", sym)
		}

		parseSide := func(rows [][]string) [][2]float64 {
			out := make([][2]float64, 0, len(rows))
			for _, r := range rows {
				if len(r) < 2 {
					continue
				}
				price, err1 := strconv.ParseFloat(r[0], 64)
				qty, err2 := strconv.ParseFloat(r[1], 64)
				if err1 != nil || err2 != nil || price <= 0 || qty <= 0 {
					continue
				}
				out = append(out, [2]float64{price, qty})
			}
			return out
		}

		bids := parseSide(payload.Bids)
		asks := parseSide(payload.Asks)
		if len(bids) == 0 || len(asks) == 0 {
			return nil, fmt.Errorf("parsed empty binance depth for %s", sym)
		}

		return &OrderBookSnapshot{
			Exchange: ex,
			Symbol:   sym,
			Bids:     bids,
			Asks:     asks,
			BestBid:  bids[0][0],
			BestAsk:  asks[0][0],
			UpdatedAt: time.Now().UTC(),
		}, nil
	case "okx":
		// OKX REST: https://www.okx.com/api/v5/market/books?instId=BTC-USDT-SWAP&sz=20
		instID := toOKXInstID(sym)
		url := fmt.Sprintf("https://www.okx.com/api/v5/market/books?instId=%s&sz=%d", instID, 20)

		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("okx depth http status=%d", resp.StatusCode)
		}

		var payload struct {
			Code string `json:"code"`
			Msg  string `json:"msg"`
			Data []struct {
				Bids [][]string `json:"bids"`
				Asks [][]string `json:"asks"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if payload.Code != "0" {
			return nil, fmt.Errorf("okx depth error code=%s msg=%s", payload.Code, payload.Msg)
		}
		if len(payload.Data) == 0 {
			return nil, fmt.Errorf("empty okx depth for %s", instID)
		}

		parseSide := func(rows [][]string) [][2]float64 {
			out := make([][2]float64, 0, len(rows))
			for _, r := range rows {
				if len(r) < 2 {
					continue
				}
				price, err1 := strconv.ParseFloat(r[0], 64)
				qty, err2 := strconv.ParseFloat(r[1], 64)
				if err1 != nil || err2 != nil || price <= 0 || qty <= 0 {
					continue
				}
				out = append(out, [2]float64{price, qty})
			}
			return out
		}

		book := payload.Data[0]
		bids := parseSide(book.Bids)
		asks := parseSide(book.Asks)
		if len(bids) == 0 || len(asks) == 0 {
			return nil, fmt.Errorf("parsed empty okx depth for %s", instID)
		}

		return &OrderBookSnapshot{
			Exchange: ex,
			Symbol:   sym,
			Bids:     bids,
			Asks:     asks,
			BestBid:  bids[0][0],
			BestAsk:  asks[0][0],
			UpdatedAt: time.Now().UTC(),
		}, nil
	default:
		return nil, fmt.Errorf("http depth fallback not implemented for exchange=%s", ex)
	}
}


