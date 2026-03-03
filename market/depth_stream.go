package market

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nofx/logger"

	"github.com/gorilla/websocket"
)

// DepthUpdate 单次深度更新（来自某个交易所 WS）
type DepthUpdate struct {
	Exchange string
	Symbol   string
	Bids     [][2]float64 // [price, qty] 已按价格排序（买盘从高到低，卖盘从低到高）
	Asks     [][2]float64
}

// OrderBookSnapshot 内存中的订单簿快照（Top N 档）
type OrderBookSnapshot struct {
	Exchange string
	Symbol   string
	Bids     [][2]float64
	Asks     [][2]float64

	BestBid float64
	BestAsk float64

	UpdatedAt time.Time
}

// DepthManager 统一管理多交易所的深度订阅与内存快照
type DepthManager struct {
	mu    sync.RWMutex
	books map[string]*OrderBookSnapshot // key: exchange|symbol

	binance *binanceDepthClient
	okx     *okxDepthClient
}

var (
	globalDepthMgr     *DepthManager
	globalDepthMgrOnce sync.Once
)

// GlobalDepthManager 返回单例 DepthManager，并在首次调用时启动 WS 客户端
func GlobalDepthManager() *DepthManager {
	globalDepthMgrOnce.Do(func() {
		m := &DepthManager{
			books: make(map[string]*OrderBookSnapshot),
		}
		m.binance = newBinanceDepthClient(m.handleDepthUpdate)
		m.okx = newOKXDepthClient(m.handleDepthUpdate)
		globalDepthMgr = m
	})
	return globalDepthMgr
}

// depthKey 规范化 map key
func depthKey(exchange, symbol string) string {
	ex := strings.ToLower(strings.TrimSpace(exchange))
	if ex == "" {
		ex = "binance"
	}
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	return ex + "|" + sym
}

// handleDepthUpdate 由各交易所客户端回调，更新内存快照
func (m *DepthManager) handleDepthUpdate(u *DepthUpdate) {
	if u == nil {
		return
	}
	if len(u.Bids) == 0 && len(u.Asks) == 0 {
		return
	}

	key := depthKey(u.Exchange, u.Symbol)

	bestBid := 0.0
	bestAsk := 0.0
	if len(u.Bids) > 0 {
		bestBid = u.Bids[0][0]
	}
	if len(u.Asks) > 0 {
		bestAsk = u.Asks[0][0]
	}

	snap := &OrderBookSnapshot{
		Exchange: strings.ToLower(strings.TrimSpace(u.Exchange)),
		Symbol:   strings.ToUpper(strings.TrimSpace(u.Symbol)),
		Bids:     u.Bids,
		Asks:     u.Asks,
		BestBid:  bestBid,
		BestAsk:  bestAsk,
		UpdatedAt: time.Now().UTC(),
	}

	m.mu.Lock()
	m.books[key] = snap
	m.mu.Unlock()
}

// GetSnapshot 返回指定交易所与币种的最新深度快照（可能是几秒前的缓存）
func (m *DepthManager) GetSnapshot(symbol, exchange string) (*OrderBookSnapshot, bool) {
	key := depthKey(exchange, symbol)
	m.mu.RLock()
	snap, ok := m.books[key]
	m.mu.RUnlock()
	return snap, ok
}

// UpdateDepthSubscriptions 根据币种列表更新订阅集合（仅在启用深度图时调用）
// exchange 目前支持 "binance" 与 "okx"（大小写不敏感）；空字符串默认为 "binance"。
func (m *DepthManager) UpdateDepthSubscriptions(symbols []string, exchange string) {
	ex := strings.ToLower(strings.TrimSpace(exchange))
	if ex == "" {
		ex = "binance"
	}
	switch ex {
	case "binance":
		if m.binance != nil {
			m.binance.UpdateSubscriptions(symbols)
		}
	case "okx":
		if m.okx != nil {
			m.okx.UpdateSubscriptions(symbols)
		}
	default:
		logger.Warnf("⚠️ DepthManager: unsupported exchange for depth subscriptions: %s", ex)
	}
}

// UpdateSubscriptions 是一个便捷包装：默认作用于 Binance；若你需要 OKX，可显式传 exchange。
func UpdateSubscriptions(symbols []string, exchange string) {
	GlobalDepthManager().UpdateDepthSubscriptions(symbols, exchange)
}

// =========================
// Binance 深度 WS 客户端
// =========================

type binanceDepthClient struct {
	url string

	mu         sync.Mutex
	conn       *websocket.Conn
	subscribed map[string]struct{} // key: symbol (大写，如 BTCUSDT)

	onUpdate func(*DepthUpdate)
}

func newBinanceDepthClient(onUpdate func(*DepthUpdate)) *binanceDepthClient {
	c := &binanceDepthClient{
		url:        "wss://fstream.binance.com/ws",
		subscribed: make(map[string]struct{}),
		onUpdate:   onUpdate,
	}
	go c.run()
	return c
}

func (c *binanceDepthClient) run() {
	for {
		conn, _, err := websocket.DefaultDialer.Dial(c.url, http.Header{})
		if err != nil {
			logger.Warnf("⚠️ Binance depth WS dial failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		logger.Infof("✓ Binance depth WS connected")

		c.mu.Lock()
		c.conn = conn
		// 重新订阅当前符号集合
		var symbols []string
		for sym := range c.subscribed {
			symbols = append(symbols, sym)
		}
		c.mu.Unlock()
		if len(symbols) > 0 {
			if err := c.sendSubscribe(symbols, true); err != nil {
				logger.Warnf("⚠️ Binance depth WS resubscribe failed: %v", err)
			}
		}

		// 读循环
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				logger.Warnf("⚠️ Binance depth WS read error: %v", err)
				break
			}
			c.handleMessage(msg)
		}

		conn.Close()

		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()

		logger.Warnf("⚠️ Binance depth WS disconnected, will retry in 5s")
		time.Sleep(5 * time.Second)
	}
}

func (c *binanceDepthClient) UpdateSubscriptions(symbols []string) {
	target := make(map[string]struct{})
	for _, s := range symbols {
		sym := strings.ToUpper(strings.TrimSpace(s))
		if sym == "" {
			continue
		}
		target[sym] = struct{}{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 计算新增与移除
	var toSubscribe, toUnsubscribe []string
	for sym := range target {
		if _, ok := c.subscribed[sym]; !ok {
			toSubscribe = append(toSubscribe, sym)
		}
	}
	for sym := range c.subscribed {
		if _, ok := target[sym]; !ok {
			toUnsubscribe = append(toUnsubscribe, sym)
		}
	}

	// 更新本地集合
	c.subscribed = target

	// 若当前无连接，仅更新集合，等待下次重连统一订阅
	if c.conn == nil {
		return
	}

	if len(toSubscribe) > 0 {
		if err := c.sendSubscribe(toSubscribe, true); err != nil {
			logger.Warnf("⚠️ Binance depth subscribe failed: %v", err)
		}
	}
	if len(toUnsubscribe) > 0 {
		if err := c.sendSubscribe(toUnsubscribe, false); err != nil {
			logger.Warnf("⚠️ Binance depth unsubscribe failed: %v", err)
		}
	}
}

func (c *binanceDepthClient) sendSubscribe(symbols []string, subscribe bool) error {
	if c.conn == nil || len(symbols) == 0 {
		return nil
	}

	params := make([]string, 0, len(symbols))
	for _, sym := range symbols {
		// depth20@100ms，符合需求
		params = append(params, strings.ToLower(sym)+"@depth20@100ms")
	}

	method := "SUBSCRIBE"
	if !subscribe {
		method = "UNSUBSCRIBE"
	}
	req := map[string]interface{}{
		"method": method,
		"params": params,
		"id":     time.Now().UnixNano() / int64(time.Millisecond),
	}

	if err := c.conn.WriteJSON(req); err != nil {
		return err
	}
	return nil
}

func (c *binanceDepthClient) handleMessage(msg []byte) {
	// 既兼容 {"stream":"...","data":{...}} 也兼容直接 data 的形式
	var envelope struct {
		Stream string          `json:"stream"`
		Data   json.RawMessage `json:"data"`
	}

	payload := msg
	if err := json.Unmarshal(msg, &envelope); err == nil && len(envelope.Data) > 0 {
		payload = envelope.Data
	}

	var evt struct {
		Symbol string     `json:"s"`
		Bids   [][]string `json:"b"`
		Asks   [][]string `json:"a"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		// 非 depth 事件，忽略
		return
	}
	symbol := strings.ToUpper(evt.Symbol)
	if symbol == "" {
		return
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

	bids := parseSide(evt.Bids)
	asks := parseSide(evt.Asks)
	if len(bids) == 0 && len(asks) == 0 {
		return
	}

	if c.onUpdate != nil {
		c.onUpdate(&DepthUpdate{
			Exchange: "binance",
			Symbol:   symbol,
			Bids:     bids,
			Asks:     asks,
		})
	}
}

// =========================
// OKX 深度 WS 客户端
// =========================

type okxDepthClient struct {
	url string

	mu         sync.Mutex
	conn       *websocket.Conn
	instSubs   map[string]struct{} // key: instId，例如 "BTC-USDT-SWAP"

	onUpdate func(*DepthUpdate)
}

func newOKXDepthClient(onUpdate func(*DepthUpdate)) *okxDepthClient {
	c := &okxDepthClient{
		url:      "wss://ws.okx.com:8443/ws/v5/public",
		instSubs: make(map[string]struct{}),
		onUpdate: onUpdate,
	}
	go c.run()
	return c
}

func (c *okxDepthClient) run() {
	for {
		conn, _, err := websocket.DefaultDialer.Dial(c.url, http.Header{})
		if err != nil {
			logger.Warnf("⚠️ OKX depth WS dial failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		logger.Infof("✓ OKX depth WS connected")

		c.mu.Lock()
		c.conn = conn
		var insts []string
		for inst := range c.instSubs {
			insts = append(insts, inst)
		}
		c.mu.Unlock()

		if len(insts) > 0 {
			if err := c.sendSubscribe(insts, true); err != nil {
				logger.Warnf("⚠️ OKX depth WS resubscribe failed: %v", err)
			}
		}

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				logger.Warnf("⚠️ OKX depth WS read error: %v", err)
				break
			}
			c.handleMessage(msg)
		}

		conn.Close()

		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()

		logger.Warnf("⚠️ OKX depth WS disconnected, will retry in 5s")
		time.Sleep(5 * time.Second)
	}
}

// toOKXInstID 将 "BTCUSDT" 之类的交易对映射为 OKX 永续合约 instId，如 "BTC-USDT-SWAP"
func toOKXInstID(symbol string) string {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if strings.HasSuffix(sym, "USDT") {
		base := strings.TrimSuffix(sym, "USDT")
		return base + "-USDT-SWAP"
	}
	return sym + "-SWAP"
}

func fromOKXInstID(instId string) string {
	// 反向映射："BTC-USDT-SWAP" -> "BTCUSDT"
	s := strings.ToUpper(strings.TrimSpace(instId))
	s = strings.TrimSuffix(s, "-SWAP")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func (c *okxDepthClient) UpdateSubscriptions(symbols []string) {
	target := make(map[string]struct{})
	for _, s := range symbols {
		inst := toOKXInstID(s)
		if inst == "" {
			continue
		}
		target[inst] = struct{}{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var toSubscribe, toUnsubscribe []string
	for inst := range target {
		if _, ok := c.instSubs[inst]; !ok {
			toSubscribe = append(toSubscribe, inst)
		}
	}
	for inst := range c.instSubs {
		if _, ok := target[inst]; !ok {
			toUnsubscribe = append(toUnsubscribe, inst)
		}
	}

	c.instSubs = target

	if c.conn == nil {
		return
	}

	if len(toSubscribe) > 0 {
		if err := c.sendSubscribe(toSubscribe, true); err != nil {
			logger.Warnf("⚠️ OKX depth subscribe failed: %v", err)
		}
	}
	if len(toUnsubscribe) > 0 {
		if err := c.sendSubscribe(toUnsubscribe, false); err != nil {
			logger.Warnf("⚠️ OKX depth unsubscribe failed: %v", err)
		}
	}
}

func (c *okxDepthClient) sendSubscribe(insts []string, subscribe bool) error {
	if c.conn == nil || len(insts) == 0 {
		return nil
	}

	op := "subscribe"
	if !subscribe {
		op = "unsubscribe"
	}

	args := make([]map[string]string, 0, len(insts))
	for _, inst := range insts {
		args = append(args, map[string]string{
			"channel": "books5",
			"instId":  inst,
		})
	}
	req := map[string]interface{}{
		"op":   op,
		"args": args,
	}
	if err := c.conn.WriteJSON(req); err != nil {
		return err
	}
	return nil
}

func (c *okxDepthClient) handleMessage(msg []byte) {
	var evt struct {
		Arg struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		} `json:"arg"`
		Data []struct {
			Bids [][]string `json:"bids"`
			Asks [][]string `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg, &evt); err != nil {
		// 不是 depth 消息，忽略
		return
	}
	if evt.Arg.Channel != "books5" || len(evt.Data) == 0 {
		return
	}
	inst := evt.Arg.InstID
	if inst == "" {
		return
	}
	symbol := fromOKXInstID(inst)
	if symbol == "" {
		return
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

	book := evt.Data[0]
	bids := parseSide(book.Bids)
	asks := parseSide(book.Asks)
	if len(bids) == 0 && len(asks) == 0 {
		return
	}

	if c.onUpdate != nil {
		c.onUpdate(&DepthUpdate{
			Exchange: "okx",
			Symbol:   symbol,
			Bids:     bids,
			Asks:     asks,
		})
	}
}

