package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nofx/logger"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	wsKeepaliveInterval      = 30 * time.Minute // Extend listenKey every 30 min (valid 60 min)
	wsReconnectDelay         = 5 * time.Second
	wsWriteWait              = 10 * time.Second
	wsPongWait               = 90 * time.Second // 延长 ReadDeadline 避免 i/o timeout 误判
	wsPingPeriod             = (wsPongWait * 9) / 10
	wsEofFallbackThreshold   = 5               // WS-API 连续 EOF 5 次后降级为 REST 轮询
	wsRestFallbackInterval   = 10 * time.Second
	wsRestFallbackRetryEvery = 60 * time.Second // fallback 期间每 60 秒重试一次 ws-api
	wsUserAgent              = "Mozilla/5.0 (Windows NT 10.0; Win64; x64)"
)

// wsDialer 自定义 Dialer：走代理、握手超时、避免裸握手被防火墙拦截
var wsDialer = websocket.Dialer{
	Proxy:            http.ProxyFromEnvironment,
	HandshakeTimeout: 10 * time.Second,
}

// wsHeaders 请求头：User-Agent 伪装，避免币安防火墙拦截裸 WebSocket 握手
func wsHeaders() http.Header {
	h := make(http.Header)
	h.Set("User-Agent", wsUserAgent)
	return h
}

func isEOF(err error) bool {
	if err == nil {
		return false
	}
	if err == io.EOF {
		return true
	}
	return strings.Contains(err.Error(), "EOF")
}

// runRestFallbackLoop 降级保护：每 10 秒 REST GetPosition 维持仓位监控；每 60 秒重试 ws-api
func (t *FuturesTrader) runRestFallbackLoop() {
	t.refreshAccountFromAPI()
	logger.Infof("[WS] REST fallback: 首次刷新完成，进入 %v 轮询模式", wsRestFallbackInterval)
	pollTicker := time.NewTicker(wsRestFallbackInterval)
	retryTicker := time.NewTicker(wsRestFallbackRetryEvery)
	defer pollTicker.Stop()
	defer retryTicker.Stop()
	for {
		select {
		case <-t.userDataStop:
			return
		case <-pollTicker.C:
			t.refreshAccountFromAPI()
			logger.Infof("[WS] REST fallback: 已刷新仓位/余额 (每 %v 轮询)", wsRestFallbackInterval)
		case <-retryTicker.C:
			apiURL := t.wsApiBaseURL()
			testConn, _, err := wsDialer.Dial(apiURL, wsHeaders())
			if err == nil {
				testConn.Close()
				logger.Infof("[WS] ws-api 恢复可用，退出 REST fallback")
				return
			}
			logger.Warnf("[WS] REST fallback 期间重试 ws-api 仍失败: %v", err)
		}
	}
}

// wsStreamBaseURL returns the WebSocket stream base URL for 连接 B（数据线）
// 仅用于 wss://fstream.binance.com/ws/{listenKey}，只监听事件，绝对不在该连接上发送 JSON-RPC
func (t *FuturesTrader) wsStreamBaseURL() string {
	if t.isTestnet {
		return "wss://stream.binancefuture.com"
	}
	return "wss://fstream.binance.com"
}

// wsApiBaseURL returns the WebSocket API base URL for 连接 A（API 线）
// 仅用于 userDataStream.start / userDataStream.ping / userDataStream.stop
func (t *FuturesTrader) wsApiBaseURL() string {
	if t.isTestnet {
		return "wss://testnet.binancefuture.com/ws-fapi/v1"
	}
	return "wss://ws-fapi.binance.com/ws-fapi/v1"
}

// wsApiUserDataStreamResponse 解析 userDataStream.start / userDataStream.ping 的 JSON 响应
type wsApiUserDataStreamResponse struct {
	ID     string `json:"id"`
	Status int    `json:"status"`
	Result struct {
		ListenKey string `json:"listenKey"`
	} `json:"result"`
}

// createListenKeyViaWS 通过 ws-api userDataStream.start 获取 listenKey（替代已废弃的 REST POST /api/v3/userDataStream）
func (t *FuturesTrader) createListenKeyViaWS(conn *websocket.Conn) (string, error) {
	req := map[string]interface{}{
		"id":     uuid.New().String(),
		"method": "userDataStream.start",
		"params": map[string]string{"apiKey": t.apiKey},
	}
	if err := conn.WriteJSON(req); err != nil {
		return "", fmt.Errorf("ws-api start write: %w", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("ws-api start read: %w", err)
	}
	var resp wsApiUserDataStreamResponse
	if err := json.Unmarshal(msg, &resp); err != nil {
		return "", fmt.Errorf("ws-api start parse: %w", err)
	}
	if resp.Status != 200 {
		return "", fmt.Errorf("ws-api start status %d", resp.Status)
	}
	listenKey := strings.TrimSpace(resp.Result.ListenKey)
	if listenKey == "" {
		return "", fmt.Errorf("empty listenKey in ws-api response")
	}
	return listenKey, nil
}

// stopUserDataStreamViaWS 通过连接 A 发送 userDataStream.stop（优雅关闭时）
func (t *FuturesTrader) stopUserDataStreamViaWS(conn *websocket.Conn) {
	req := map[string]interface{}{
		"id":     uuid.New().String(),
		"method": "userDataStream.stop",
		"params": map[string]string{"apiKey": t.apiKey},
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = conn.WriteJSON(req)
}

// keepaliveListenKeyViaREST 通过 REST PUT /fapi/v1/listenKey 续期 listenKey（ws-api 失败时的备用）
func (t *FuturesTrader) keepaliveListenKeyViaREST(listenKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return t.client.NewKeepaliveUserStreamService().ListenKey(listenKey).Do(ctx)
}

// keepaliveListenKeyViaWS 通过连接 A 的 userDataStream.ping 续期 listenKey
func (t *FuturesTrader) keepaliveListenKeyViaWS(conn *websocket.Conn) error {
	req := map[string]interface{}{
		"id":     uuid.New().String(),
		"method": "userDataStream.ping",
		"params": map[string]string{"apiKey": t.apiKey},
	}
	if err := conn.WriteJSON(req); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	var resp wsApiUserDataStreamResponse
	if err := json.Unmarshal(msg, &resp); err != nil {
		return err
	}
	if resp.Status != 200 {
		return fmt.Errorf("ws-api ping status %d", resp.Status)
	}
	return nil
}

// wsAccountUpdate represents ACCOUNT_UPDATE event (e: "ACCOUNT_UPDATE")
// a.B = balances, a.P = positions (delta only - changed items)
type wsAccountUpdate struct {
	EventType string `json:"e"`
	EventTime int64  `json:"E"`
	TransTime int64  `json:"T"`
	UpdateData struct {
		Reason    string `json:"m"`
		Balances  []struct {
			Asset    string `json:"a"`
			WB       string `json:"wb"`
			CW       string `json:"cw"`
			BC       string `json:"bc"`
		} `json:"B"`
		Positions []struct {
			Symbol           string `json:"s"`
			PositionAmt      string `json:"pa"`
			EntryPrice       string `json:"ep"`
			BEP              string `json:"bep"`
			UnrealizedProfit string `json:"up"`
			MarginType       string `json:"mt"`
			IsolatedWallet   string `json:"iw"`
			PositionSide     string `json:"ps"`
		} `json:"P"`
	} `json:"a"`
}

// wsOrderTradeUpdate represents ORDER_TRADE_UPDATE (e: "ORDER_TRADE_UPDATE")
// Can trigger position/balance change when order is filled
type wsOrderTradeUpdate struct {
	EventType string `json:"e"`
	EventTime int64  `json:"E"`
	Order     struct {
		Symbol       string `json:"s"`
		Side         string `json:"S"`
		PositionSide string `json:"ps"`
		Status       string `json:"X"`
		FilledQty    string `json:"z"`
		AvgPrice     string `json:"ap"`
		RealizedPnL  string `json:"rp"`
	} `json:"o"`
}

// StartUserDataStream starts the User Data Stream WebSocket in the background.
// Updates cached balance and positions in real time. Automatically reconnects on disconnect.
func (t *FuturesTrader) StartUserDataStream() {
	if t.userDataStop != nil {
		return // Already running
	}
	t.userDataStop = make(chan struct{})
	go t.runUserDataStream()
}

// StopUserDataStream stops the User Data Stream.
func (t *FuturesTrader) StopUserDataStream() {
	if t.userDataStop != nil {
		close(t.userDataStop)
		t.userDataStop = nil
	}
}

func (t *FuturesTrader) runUserDataStream() {
	eofCount := 0
	for {
		select {
		case <-t.userDataStop:
			return
		default:
		}

		// 1. 连接 A（ws-fapi）：仅用于 userDataStream.start / ping / stop
		apiURL := t.wsApiBaseURL()
		logger.Infof("[WS] Connecting ws-fapi (API线) %s", apiURL)
		apiConn, _, err := wsDialer.Dial(apiURL, wsHeaders())
		if err != nil {
			if isEOF(err) {
				eofCount++
				logger.Warnf("[WS] ws-api connect EOF (consecutive %d/%d): %v", eofCount, wsEofFallbackThreshold, err)
				if eofCount >= wsEofFallbackThreshold {
					logger.Errorf("[WS] ⚠️ 严重：ws-api 连续 %d 次 EOF，降级为 REST 轮询维持仓位监控", wsEofFallbackThreshold)
					t.runRestFallbackLoop()
					eofCount = 0
					continue
				}
			} else {
				eofCount = 0
			}
			logger.Warnf("[WS] ws-api connect failed: %v, retry in %v", err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}
		eofCount = 0

		listenKey, err := t.createListenKeyViaWS(apiConn)
		if err != nil {
			apiConn.Close()
			logger.Warnf("[WS] userDataStream.start failed: %v, retry in %v", err, wsReconnectDelay)
			time.Sleep(wsReconnectDelay)
			continue
		}

		// 2. 连接 B（fstream）：wss://fstream.binance.com/ws/{listenKey}，仅监听事件，不发送 JSON-RPC
		wsURL := t.wsStreamBaseURL() + "/ws/" + listenKey
		logger.Infof("[WS] Connecting fstream (数据线) %s", strings.Replace(wsURL, listenKey, "<key>", 1))

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		var streamConn *websocket.Conn
		var streamConnMu sync.Mutex

		go func() {
			ticker := time.NewTicker(wsKeepaliveInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := t.keepaliveListenKeyViaWS(apiConn); err != nil {
						// ws-api 失败时尝试 REST PUT /fapi/v1/listenKey 续期
						if restErr := t.keepaliveListenKeyViaREST(listenKey); restErr == nil {
							logger.Infof("[WS] ListenKey keepalive OK via REST (30 min)")
							continue
						}
						// 两者均失败：重连 A 并重新 start 获取新 key
						logger.Warnf("[WS] userDataStream.ping and REST keepalive failed: %v, reconnecting API line", err)
						apiConn.Close()
						streamConnMu.Lock()
						if streamConn != nil {
							streamConn.Close()
						}
						streamConnMu.Unlock()
						cancel()
						return
					}
					logger.Infof("[WS] ListenKey keepalive OK (30 min)")
				}
			}
		}()

		conn, _, err := wsDialer.DialContext(ctx, wsURL, wsHeaders())
		if err != nil {
			logger.Warnf("[WS] User Data Stream connect failed: %v", err)
			apiConn.Close()
			cancel()
			time.Sleep(wsReconnectDelay)
			continue
		}
		streamConnMu.Lock()
		streamConn = conn
		streamConnMu.Unlock()

		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(wsPongWait))
			return nil
		})

		// Initial sync: REST only AFTER WS connected (avoids gap where trades could be missed)
		// Correct order: WS connect -> immediate REST sync -> then event-driven updates
		t.refreshAccountFromAPI()

		// Ping goroutine
		go func() {
			ticker := time.NewTicker(wsPingPeriod)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
					if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
						return
					}
				}
			}
		}()

		// Read loop
		func() {
			defer conn.Close()
			defer apiConn.Close()
			defer cancel()
			defer close(done)

			for {
				select {
				case <-t.userDataStop:
					t.stopUserDataStreamViaWS(apiConn)
					return
				default:
				}

				conn.SetReadDeadline(time.Now().Add(wsPongWait))
				_, msg, err := conn.ReadMessage()
				if err != nil {
					if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						logger.Warnf("[WS] User Data Stream read error: %v", err)
					}
					return
				}

				t.handleUserDataMessage(msg)
			}
		}()

		<-done
		logger.Infof("[WS] User Data Stream disconnected, reconnecting in %v", wsReconnectDelay)
		time.Sleep(wsReconnectDelay)
	}
}

func (t *FuturesTrader) handleUserDataMessage(msg []byte) {
	var generic struct {
		EventType string `json:"e"`
	}
	if err := json.Unmarshal(msg, &generic); err != nil {
		return
	}

	switch generic.EventType {
	case "ACCOUNT_UPDATE":
		var ev wsAccountUpdate
		if err := json.Unmarshal(msg, &ev); err != nil {
			return
		}
		t.applyAccountUpdate(&ev)
	case "ORDER_TRADE_UPDATE":
		var ev wsOrderTradeUpdate
		if err := json.Unmarshal(msg, &ev); err != nil {
			return
		}
		t.applyOrderTradeUpdate(&ev)
	}
}

func (t *FuturesTrader) applyAccountUpdate(ev *wsAccountUpdate) {
	// Event-driven: merge delta into in-memory cache, NO REST
	// Handles both ORDER (trade fill) and FUNDING_FEE (资金费): same structure, apply balance/position updates
	// Build new map and replace (no in-place mutate) to avoid race with GetBalance readers
	t.balanceCacheMutex.Lock()
	newBal := make(map[string]interface{})
	for k, v := range t.cachedBalance {
		newBal[k] = v
	}
	for _, b := range ev.UpdateData.Balances {
		wb, _ := strconv.ParseFloat(b.WB, 64)
		cw, _ := strconv.ParseFloat(b.CW, 64)
		if b.Asset == "USDT" {
			newBal["totalWalletBalance"] = wb
			newBal["availableBalance"] = cw
		}
	}
	t.cachedBalance = newBal
	t.balanceCacheTime = time.Now()
	t.balanceCacheMutex.Unlock()

	// Merge positions delta
	t.positionsCacheMutex.Lock()
	posMap := make(map[string]map[string]interface{})
	for _, p := range t.cachedPositions {
		sym, sok := p["symbol"].(string)
		side, _ := p["side"].(string)
		if !sok {
			continue
		}
		posSide := "BOTH"
		if side == "long" {
			posSide = "LONG"
		} else if side == "short" {
			posSide = "SHORT"
		}
		posMap[sym+"_"+posSide] = p
	}
	for _, p := range ev.UpdateData.Positions {
		pa, _ := strconv.ParseFloat(p.PositionAmt, 64)
		key := p.Symbol + "_" + p.PositionSide
		if pa == 0 {
			delete(posMap, key)
			continue
		}
		ep, _ := strconv.ParseFloat(p.EntryPrice, 64)
		up, _ := strconv.ParseFloat(p.UnrealizedProfit, 64)
		pm := map[string]interface{}{
			"symbol":           p.Symbol,
			"positionAmt":      pa,
			"entryPrice":       ep,
			"unRealizedProfit": up,
			"side":             "long",
		}
		if pa < 0 {
			pm["side"] = "short"
		}
		posMap[key] = pm
	}
	var result []map[string]interface{}
	for _, pm := range posMap {
		result = append(result, pm)
	}
	t.cachedPositions = result
	t.positionsCacheTime = time.Now()
	t.positionsCacheMutex.Unlock()

	// Log (reason m: ORDER=trade fill, FUNDING_FEE=资金费; both handled identically)
	wb := 0.0
	t.balanceCacheMutex.RLock()
	if v, ok := t.cachedBalance["totalWalletBalance"].(float64); ok {
		wb = v
	}
	reason := ev.UpdateData.Reason
	t.balanceCacheMutex.RUnlock()
	if reason == "FUNDING_FEE" {
		logger.Infof("[WS] 📥 Received ACCOUNT_UPDATE (FUNDING_FEE): Balance updated to %.2f USDT.", wb)
	} else {
		logger.Infof("[WS] 📥 Received ACCOUNT_UPDATE: Balance updated to %.2f USDT.", wb)
	}
}

func (t *FuturesTrader) applyOrderTradeUpdate(ev *wsOrderTradeUpdate) {
	if ev.Order.Status != "FILLED" {
		return
	}
	filled, _ := strconv.ParseFloat(ev.Order.FilledQty, 64)
	if filled == 0 {
		return
	}
	avgPrice, _ := strconv.ParseFloat(ev.Order.AvgPrice, 64)
	key := ev.Order.Symbol + "_" + ev.Order.PositionSide

	t.positionsCacheMutex.Lock()
	posMap := make(map[string]map[string]interface{})
	for _, p := range t.cachedPositions {
		sym, sok := p["symbol"].(string)
		side, _ := p["side"].(string)
		if !sok {
			continue
		}
		posSide := "BOTH"
		if side == "long" {
			posSide = "LONG"
		} else if side == "short" {
			posSide = "SHORT"
		}
		posMap[sym+"_"+posSide] = p
	}
	cur, _ := posMap[key]
	var newAmt float64
	if cur != nil {
		pa, _ := cur["positionAmt"].(float64)
		if ev.Order.Side == "BUY" {
			newAmt = pa + filled
		} else {
			newAmt = pa - filled
		}
	} else {
		if ev.Order.Side == "BUY" {
			newAmt = filled
		} else {
			newAmt = -filled
		}
	}
	if newAmt == 0 {
		delete(posMap, key)
	} else {
		posMap[key] = map[string]interface{}{
			"symbol":           ev.Order.Symbol,
			"positionAmt":      newAmt,
			"entryPrice":       avgPrice,
			"unRealizedProfit": 0.0,
			"side":             "long",
		}
		if newAmt < 0 {
			posMap[key]["side"] = "short"
		}
	}
	var result []map[string]interface{}
	for _, pm := range posMap {
		result = append(result, pm)
	}
	t.cachedPositions = result
	t.positionsCacheTime = time.Now()
	t.positionsCacheMutex.Unlock()

	logger.Infof("[WS] 📥 Received ORDER_TRADE_UPDATE: Position %s %s updated.", ev.Order.Symbol, ev.Order.PositionSide)
}

// refreshAccountFromAPI fetches full account and positions via REST and updates cache.
func (t *FuturesTrader) refreshAccountFromAPI() {
	if err := CheckCircuitBreaker(); err != nil {
		logger.Warnf("[WS] Circuit breaker open, skip refresh: %v", err)
		return
	}
	account, err := t.client.NewGetAccountService().Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		logger.Warnf("[WS] Initial sync failed: %v", err)
		return
	}

	bal := make(map[string]interface{})
	bal["totalWalletBalance"], _ = strconv.ParseFloat(account.TotalWalletBalance, 64)
	bal["availableBalance"], _ = strconv.ParseFloat(account.AvailableBalance, 64)
	bal["totalUnrealizedProfit"], _ = strconv.ParseFloat(account.TotalUnrealizedProfit, 64)

	t.balanceCacheMutex.Lock()
	t.cachedBalance = bal
	t.balanceCacheTime = time.Now()
	t.balanceCacheMutex.Unlock()

	positions, err := t.client.NewGetPositionRiskService().Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		posAmt, _ := strconv.ParseFloat(pos.PositionAmt, 64)
		if posAmt == 0 {
			continue
		}
		posMap := make(map[string]interface{})
		posMap["symbol"] = pos.Symbol
		posMap["positionAmt"], _ = strconv.ParseFloat(pos.PositionAmt, 64)
		posMap["entryPrice"], _ = strconv.ParseFloat(pos.EntryPrice, 64)
		posMap["markPrice"], _ = strconv.ParseFloat(pos.MarkPrice, 64)
		posMap["unRealizedProfit"], _ = strconv.ParseFloat(pos.UnRealizedProfit, 64)
		posMap["leverage"], _ = strconv.ParseFloat(pos.Leverage, 64)
		posMap["liquidationPrice"], _ = strconv.ParseFloat(pos.LiquidationPrice, 64)
		if posAmt > 0 {
			posMap["side"] = "long"
		} else {
			posMap["side"] = "short"
		}
		result = append(result, posMap)
	}

	t.positionsCacheMutex.Lock()
	t.cachedPositions = result
	t.positionsCacheTime = time.Now()
	t.positionsCacheMutex.Unlock()
}
