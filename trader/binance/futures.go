package binance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"nofx/hook"
	"nofx/logger"
	"nofx/syncer"
	"nofx/trader/types"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

// getBrOrderID generates unique order ID (for futures contracts)
// Format: x-{BR_ID}{TIMESTAMP}{RANDOM}
// Futures limit is 32 characters, use this limit consistently
// Uses nanosecond timestamp + random number to ensure global uniqueness (collision probability < 10^-20)
func getBrOrderID() string {
	brID := "KzrpZaP9" // Futures br ID

	// Calculate available space: 32 - len("x-KzrpZaP9") = 32 - 11 = 21 characters
	// Allocation: 13-digit timestamp + 8-digit random = 21 characters (perfect utilization)
	timestamp := time.Now().UnixNano() % 10000000000000 // 13-digit nanosecond timestamp

	// Generate 4-byte random number (8 hex digits)
	randomBytes := make([]byte, 4)
	rand.Read(randomBytes)
	randomHex := hex.EncodeToString(randomBytes)

	// Format: x-KzrpZaP9{13-digit timestamp}{8-digit random}
	// Example: x-KzrpZaP91234567890123abcdef12 (exactly 31 characters)
	orderID := fmt.Sprintf("x-%s%d%s", brID, timestamp, randomHex)

	// Ensure not exceeding 32-character limit (theoretically exactly 31 characters)
	if len(orderID) > 32 {
		orderID = orderID[:32]
	}

	return orderID
}

// FuturesTrader Binance futures trader
type FuturesTrader struct {
	client  *futures.Client
	apiKey  string // 用于 ws-api userDataStream.start / userDataStream.ping，替代废弃的 REST listenKey 接口
	isTestnet bool

	// Balance cache (updated by WebSocket User Data Stream or HTTP fallback)
	cachedBalance     map[string]interface{}
	balanceCacheTime  time.Time
	balanceCacheMutex sync.RWMutex

	// Position cache (updated by WebSocket User Data Stream or HTTP fallback)
	cachedPositions     []map[string]interface{}
	positionsCacheTime  time.Time
	positionsCacheMutex sync.RWMutex

	// Cache validity period (15 seconds) - used only when User Data Stream is not active
	cacheDuration time.Duration

	// User Data Stream control
	userDataStop chan struct{}

	// Global account key for shared sync/cache
	accountKey string

	// Global OrderSync subscription info
	orderSyncExchangeID string
	orderSyncTraderID   string
}

const binanceFuturesTestnetURL = "https://testnet.binancefuture.com"

// NewFuturesTrader creates futures trader with User Data Stream for real-time cache.
// isTestnet: true 使用 testnet.binancefuture.com 模拟盘
func NewFuturesTrader(apiKey, secretKey string, userId string, isTestnet bool) *FuturesTrader {
	return newFuturesTrader(apiKey, secretKey, userId, isTestnet, true)
}

// NewFuturesTraderNoUserStream creates futures trader without starting User Data Stream.
// Use for short-lived temp traders (e.g. API account fetch) to avoid WebSocket overhead.
func NewFuturesTraderNoUserStream(apiKey, secretKey string, userId string, isTestnet bool) *FuturesTrader {
	return newFuturesTrader(apiKey, secretKey, userId, isTestnet, false)
}

const (
	recvWindowMs      = 20000 // 20s for -1021 (go-binance may not expose RecvWindow on all services)
	timeSyncThreshold = 5000  // 5s, force sync if offset exceeds
)

func newFuturesTrader(apiKey, secretKey string, userId string, isTestnet bool, startUserStream bool) *FuturesTrader {
	client := futures.NewClient(apiKey, secretKey)
	if isTestnet {
		client.BaseURL = binanceFuturesTestnetURL
		logger.Infof("✓ Binance Futures using testnet: %s", client.BaseURL)
	}

	hookRes := hook.HookExec[hook.NewBinanceTraderResult](hook.NEW_BINANCE_TRADER, userId, client)
	if hookRes != nil && hookRes.GetResult() != nil {
		client = hookRes.GetResult()
	}
	// 全局强制：若为模拟盘，防止 hook 覆盖 BaseURL，确保余额/下单等所有请求走测试网
	if isTestnet && client != nil {
		client.BaseURL = binanceFuturesTestnetURL
	}

	// Sync time to avoid "Timestamp ahead" error
	syncBinanceServerTime(client)
	trader := &FuturesTrader{
		client:        client,
		apiKey:        apiKey,
		isTestnet:     isTestnet,
		cacheDuration: 15 * time.Second, // 15-second cache (fallback when WebSocket inactive)
	}

	// Set dual-side position mode (Hedge Mode)
	// This is required because the code uses PositionSide (LONG/SHORT)
	if err := trader.setDualSidePosition(); err != nil {
		logger.Infof("⚠️ Failed to set dual-side position mode: %v (ignore this warning if already in dual-side mode)", err)
	}

	if startUserStream {
		trader.StartUserDataStream()
	}

	return trader
}

// SetAccountKey sets the global account key (exchange account UUID preferred).
func (t *FuturesTrader) SetAccountKey(key string) {
	t.accountKey = key
}

// BuildExchangeAccountKey builds the shared cache key for a Binance exchange config.
func BuildExchangeAccountKey(exchangeID string, isTestnet bool) string {
	if exchangeID == "" {
		return ""
	}
	return fmt.Sprintf("binance:%s:%t", exchangeID, isTestnet)
}

func (t *FuturesTrader) accountCacheKey() string {
	if t.accountKey != "" {
		return t.accountKey
	}
	if t.apiKey != "" {
		return fmt.Sprintf("binance:%s:%t", t.apiKey, t.isTestnet)
	}
	return ""
}

func (t *FuturesTrader) publishBalanceCache(balance map[string]interface{}) {
	key := t.accountCacheKey()
	if key == "" {
		return
	}
	syncer.GetGlobalSyncManager().SetBalance(key, balance)
}

func (t *FuturesTrader) publishPositionsCache(positions []map[string]interface{}) {
	key := t.accountCacheKey()
	if key == "" {
		return
	}
	syncer.GetGlobalSyncManager().SetPositions(key, positions)
}

// setDualSidePosition sets dual-side position mode (called during initialization)
func (t *FuturesTrader) setDualSidePosition() error {
	if err := CheckCircuitBreaker(); err != nil {
		return err
	}
	// Try to set dual-side position mode
	err := t.client.NewChangePositionModeService().
		DualSide(true). // true = dual-side position (Hedge Mode)
		Do(context.Background())

	if err != nil {
		// If error message contains "No need to change", it means already in dual-side position mode
		if strings.Contains(err.Error(), "No need to change position side") {
			logger.Infof("  ✓ Account is already in dual-side position mode (Hedge Mode)")
			return nil
		}
		// Other errors are returned (but won't interrupt initialization in the caller)
		return err
	}

	logger.Infof("  ✓ Account has been switched to dual-side position mode (Hedge Mode)")
	logger.Infof("  ℹ️  Dual-side position mode allows holding both long and short positions simultaneously")
	return nil
}

// syncBinanceServerTime syncs Binance server time to ensure request timestamps are valid
func syncBinanceServerTime(client *futures.Client) {
	if err := CheckCircuitBreaker(); err != nil {
		return
	}
	serverTime, err := client.NewServerTimeService().Do(context.Background())
	if err != nil {
		logger.Infof("⚠️ Failed to sync Binance server time: %v", err)
		return
	}

	now := time.Now().UnixMilli()
	offset := now - serverTime
	client.TimeOffset = offset
	logger.Infof("⏱ Binance server time synced, offset %dms", offset)
}

// ensureTimeSync forces sync if offset exceeds threshold (mitigate -1021)
func (t *FuturesTrader) ensureTimeSync() {
	if err := CheckCircuitBreaker(); err != nil {
		return
	}
	serverTime, err := t.client.NewServerTimeService().Do(context.Background())
	if err != nil {
		return
	}
	offset := time.Now().UnixMilli() - serverTime
	if offset > timeSyncThreshold || offset < -timeSyncThreshold {
		t.client.TimeOffset = offset
		logger.Infof("⏱ Time sync forced (offset %dms > %dms)", offset, timeSyncThreshold)
	}
}

// GetBalance gets account balance. Returns from local cache (updated by User Data Stream)
// without HTTP. Falls back to HTTP only when cache is empty.
// Returns a copy to avoid race: reader holds copy while WS may replace cache.
func (t *FuturesTrader) GetBalance() (map[string]interface{}, error) {
	if key := t.accountCacheKey(); key != "" {
		if cached, ok := syncer.GetGlobalSyncManager().GetBalance(key); ok {
			return cached, nil
		}
	}

	if err := CheckCircuitBreaker(); err != nil {
		t.balanceCacheMutex.RLock()
		if t.cachedBalance != nil {
			snapshot := make(map[string]interface{}, len(t.cachedBalance))
			for k, v := range t.cachedBalance {
				snapshot[k] = v
			}
			t.balanceCacheMutex.RUnlock()
			return snapshot, nil
		}
		t.balanceCacheMutex.RUnlock()
		return nil, err
	}

	t.balanceCacheMutex.RLock()
	if t.cachedBalance != nil {
		snapshot := make(map[string]interface{}, len(t.cachedBalance))
		for k, v := range t.cachedBalance {
			snapshot[k] = v
		}
		t.balanceCacheMutex.RUnlock()
		t.publishBalanceCache(snapshot)
		return snapshot, nil
	}
	t.balanceCacheMutex.RUnlock()

	// Cache empty: force HTTP fetch and update cache
	return t.fetchAndCacheBalance()
}

// GetBalanceFromCache returns cached balance only (no REST).
func (t *FuturesTrader) GetBalanceFromCache() (map[string]interface{}, bool) {
	if key := t.accountCacheKey(); key != "" {
		if cached, ok := syncer.GetGlobalSyncManager().GetBalance(key); ok {
			return cached, true
		}
	}
	t.balanceCacheMutex.RLock()
	if t.cachedBalance != nil {
		snapshot := make(map[string]interface{}, len(t.cachedBalance))
		for k, v := range t.cachedBalance {
			snapshot[k] = v
		}
		t.balanceCacheMutex.RUnlock()
		return snapshot, true
	}
	t.balanceCacheMutex.RUnlock()
	return nil, false
}

// fetchAndCacheBalance fetches balance via REST API and updates cache
func (t *FuturesTrader) fetchAndCacheBalance() (map[string]interface{}, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	account, err := t.client.NewGetAccountService().Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	result := make(map[string]interface{})
	result["totalWalletBalance"], _ = strconv.ParseFloat(account.TotalWalletBalance, 64)
	result["availableBalance"], _ = strconv.ParseFloat(account.AvailableBalance, 64)
	result["totalUnrealizedProfit"], _ = strconv.ParseFloat(account.TotalUnrealizedProfit, 64)

	t.balanceCacheMutex.Lock()
	t.cachedBalance = result
	t.balanceCacheTime = time.Now()
	t.balanceCacheMutex.Unlock()
	t.publishBalanceCache(result)

	return result, nil
}

// GetPositions gets all positions. Returns from local cache (updated by User Data Stream)
// without HTTP. Falls back to HTTP only when cache is empty.
// Returns a copy to avoid race: reader holds copy while WS may replace cache.
func (t *FuturesTrader) GetPositions() ([]map[string]interface{}, error) {
	if key := t.accountCacheKey(); key != "" {
		if cached, ok := syncer.GetGlobalSyncManager().GetPositions(key, 0); ok {
			return cached, nil
		}
	}

	t.positionsCacheMutex.RLock()
	if t.cachedPositions != nil {
		snapshot := make([]map[string]interface{}, len(t.cachedPositions))
		for i, p := range t.cachedPositions {
			pm := make(map[string]interface{}, len(p))
			for k, v := range p {
				pm[k] = v
			}
			snapshot[i] = pm
		}
		t.positionsCacheMutex.RUnlock()
		t.publishPositionsCache(snapshot)
		return snapshot, nil
	}
	t.positionsCacheMutex.RUnlock()

	// Cache empty: force HTTP fetch and update cache
	return t.fetchAndCachePositions()
}

// GetPositionsFromCache returns cached positions only (no REST).
func (t *FuturesTrader) GetPositionsFromCache() ([]map[string]interface{}, bool) {
	if key := t.accountCacheKey(); key != "" {
		if cached, ok := syncer.GetGlobalSyncManager().GetPositions(key, 365*24*time.Hour); ok {
			return cached, true
		}
	}
	t.positionsCacheMutex.RLock()
	if t.cachedPositions != nil {
		snapshot := make([]map[string]interface{}, len(t.cachedPositions))
		for i, p := range t.cachedPositions {
			pm := make(map[string]interface{}, len(p))
			for k, v := range p {
				pm[k] = v
			}
			snapshot[i] = pm
		}
		t.positionsCacheMutex.RUnlock()
		return snapshot, true
	}
	t.positionsCacheMutex.RUnlock()
	return nil, false
}

// GetPositionsFromCacheMaxAge returns cached positions only when they are recent enough.
func (t *FuturesTrader) GetPositionsFromCacheMaxAge(maxAge time.Duration) ([]map[string]interface{}, bool) {
	if key := t.accountCacheKey(); key != "" {
		if cached, ok := syncer.GetGlobalSyncManager().GetPositions(key, maxAge); ok {
			return cached, true
		}
	}
	if maxAge <= 0 {
		maxAge = 5 * time.Second
	}
	t.positionsCacheMutex.RLock()
	defer t.positionsCacheMutex.RUnlock()
	if t.cachedPositions == nil || time.Since(t.positionsCacheTime) > maxAge {
		return nil, false
	}
	snapshot := make([]map[string]interface{}, len(t.cachedPositions))
	for i, p := range t.cachedPositions {
		pm := make(map[string]interface{}, len(p))
		for k, v := range p {
			pm[k] = v
		}
		snapshot[i] = pm
	}
	return snapshot, true
}

// fetchAndCachePositions fetches positions via REST API and updates cache
func (t *FuturesTrader) fetchAndCachePositions() ([]map[string]interface{}, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	positions, err := t.client.NewGetPositionRiskService().Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return nil, fmt.Errorf("failed to get positions: %w", err)
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
	t.publishPositionsCache(result)

	return result, nil
}

// ReconcileFromREST 强制对账：用 REST 全量数据覆盖本地缓存（缓存自我纠错，应对 WS 漏接或手机端手动操作）
func (t *FuturesTrader) ReconcileFromREST() {
	t.refreshAccountFromAPI()
}

// SetMarginMode sets margin mode
func (t *FuturesTrader) SetMarginMode(symbol string, isCrossMargin bool) error {
	var marginType futures.MarginType
	if isCrossMargin {
		marginType = futures.MarginTypeCrossed
	} else {
		marginType = futures.MarginTypeIsolated
	}

	// Try to set margin mode
	err := t.client.NewChangeMarginTypeService().
		Symbol(symbol).
		MarginType(marginType).
		Do(context.Background())

	marginModeStr := "Cross Margin"
	if !isCrossMargin {
		marginModeStr = "Isolated Margin"
	}

	if err != nil {
		// 币安 -4046: "No need to change margin type" 表示已是目标模式，忽略并继续
		if contains(err.Error(), "No need to change margin type") || contains(err.Error(), "-4046") || contains(err.Error(), "4046") {
			logger.Infof("  ✓ %s margin mode is already %s", symbol, marginModeStr)
			return nil
		}
		// If there is an open position, margin mode cannot be changed, but this doesn't affect trading
		if contains(err.Error(), "Margin type cannot be changed if there exists position") {
			logger.Infof("  ⚠️ %s has open positions, cannot change margin mode, continuing with current mode", symbol)
			return nil
		}
		// Detect Multi-Assets mode (error code -4168)
		if contains(err.Error(), "Multi-Assets mode") || contains(err.Error(), "-4168") || contains(err.Error(), "4168") {
			logger.Infof("  ⚠️ %s detected Multi-Assets mode, forcing Cross Margin mode", symbol)
			logger.Infof("  💡 Tip: To use Isolated Margin mode, please disable Multi-Assets mode in Binance")
			return nil
		}
		// Detect Unified Account API (Portfolio Margin)
		if contains(err.Error(), "unified") || contains(err.Error(), "portfolio") || contains(err.Error(), "Portfolio") {
			logger.Infof("  ❌ %s detected Unified Account API, unable to trade futures", symbol)
			return fmt.Errorf("please use 'Spot & Futures Trading' API permission, do not use 'Unified Account API'")
		}
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Don't return error, let trading continue
		return nil
	}

	logger.Infof("  ✓ %s margin mode set to %s", symbol, marginModeStr)
	return nil
}

// SetLeverage sets leverage (with smart detection and cooldown period)
func (t *FuturesTrader) SetLeverage(symbol string, leverage int) error {
	// First try to get current leverage (from position information)
	currentLeverage := 0
	positions, err := t.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == symbol {
				if lev, ok := pos["leverage"].(float64); ok {
					currentLeverage = int(lev)
					break
				}
			}
		}
	}

	// If current leverage is already the target leverage, skip
	if currentLeverage == leverage && currentLeverage > 0 {
		logger.Infof("  ✓ %s leverage is already %dx, no need to change", symbol, leverage)
		return nil
	}

	// Change leverage
	_, err = t.client.NewChangeLeverageService().
		Symbol(symbol).
		Leverage(leverage).
		Do(context.Background())

	if err != nil {
		// If error message contains "No need to change", leverage is already the target value
		if contains(err.Error(), "No need to change") {
			logger.Infof("  ✓ %s leverage is already %dx", symbol, leverage)
			return nil
		}
		return fmt.Errorf("failed to set leverage: %w", err)
	}

	logger.Infof("  ✓ %s leverage changed to %dx", symbol, leverage)

	// Wait 5 seconds after changing leverage (to avoid cooldown period errors)
	logger.Infof("  ⏱ Waiting 5 seconds for cooldown period...")
	time.Sleep(5 * time.Second)

	return nil
}

// OpenLong opens a long position
func (t *FuturesTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	// First cancel all pending orders for this symbol (clean up old stop-loss and take-profit orders)
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel old pending orders (may not have any): %v", err)
	}

	// Set leverage
	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	// Note: Margin mode should be set by the caller (AutoTrader) before opening position via SetMarginMode

	// Format quantity to correct precision
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// Check if formatted quantity is 0 (prevent rounding errors)
	quantityFloat, parseErr := strconv.ParseFloat(quantityStr, 64)
	if parseErr != nil || quantityFloat <= 0 {
		return nil, fmt.Errorf("position size too small, rounded to 0 (original: %.8f → formatted: %s). Suggest increasing position amount or selecting a lower-priced coin", quantity, quantityStr)
	}

	// Check minimum notional value (Binance requires at least 10 USDT)
	if err := t.CheckMinNotional(symbol, quantityFloat); err != nil {
		return nil, err
	}

	// Create market buy order (using br ID)
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeBuy).
		PositionSide(futures.PositionSideTypeLong).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("failed to open long position: %w", err)
	}

	logger.Infof("✓ Opened long position successfully: %s quantity: %s", symbol, quantityStr)
	logger.Infof("  Order ID: %d", order.OrderID)

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// OpenShort opens a short position
func (t *FuturesTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	// First cancel all pending orders for this symbol (clean up old stop-loss and take-profit orders)
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel old pending orders (may not have any): %v", err)
	}

	// Set leverage
	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	// Note: Margin mode should be set by the caller (AutoTrader) before opening position via SetMarginMode

	// Format quantity to correct precision
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// Check if formatted quantity is 0 (prevent rounding errors)
	quantityFloat, parseErr := strconv.ParseFloat(quantityStr, 64)
	if parseErr != nil || quantityFloat <= 0 {
		return nil, fmt.Errorf("position size too small, rounded to 0 (original: %.8f → formatted: %s). Suggest increasing position amount or selecting a lower-priced coin", quantity, quantityStr)
	}

	// Check minimum notional value (Binance requires at least 10 USDT)
	if err := t.CheckMinNotional(symbol, quantityFloat); err != nil {
		return nil, err
	}

	// Create market sell order (using br ID)
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeSell).
		PositionSide(futures.PositionSideTypeShort).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("failed to open short position: %w", err)
	}

	logger.Infof("✓ Opened short position successfully: %s quantity: %s", symbol, quantityStr)
	logger.Infof("  Order ID: %d", order.OrderID)

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// ForceZeroPositionInCache 平仓成功后或 alreadyClosed 时清理幽灵缓存，将该 symbol 的对应仓位从内存移除（对外暴露供 AutoTrader 调用）
func (t *FuturesTrader) ForceZeroPositionInCache(symbol, positionSide string) {
	key := symbol + "_" + positionSide
	t.positionsCacheMutex.Lock()
	defer t.positionsCacheMutex.Unlock()
	newList := make([]map[string]interface{}, 0, len(t.cachedPositions))
	for _, p := range t.cachedPositions {
		psym, _ := p["symbol"].(string)
		pside := "LONG"
		if s, _ := p["side"].(string); s == "short" {
			pside = "SHORT"
		}
		if psym+"_"+pside == key {
			continue // 移除该仓位
		}
		newList = append(newList, p)
	}
	t.cachedPositions = newList
	t.positionsCacheTime = time.Now()
	snapshot := make([]map[string]interface{}, len(t.cachedPositions))
	for i, p := range t.cachedPositions {
		pm := make(map[string]interface{}, len(p))
		for k, v := range p {
			pm[k] = v
		}
		snapshot[i] = pm
	}
	t.publishPositionsCache(snapshot)
}

// getPositionSizeForClose returns absolute position size from WS cache, floored. 0 if not found.
// 注意：平仓操作应使用 getPositionSizeFromREST 忽略缓存，此方法仅用于非平仓场景
func (t *FuturesTrader) getPositionSizeForClose(symbol, side string) float64 {
	positions, err := t.GetPositions()
	if err != nil {
		return 0
	}
	for _, pos := range positions {
		psym, _ := pos["symbol"].(string)
		pside, _ := pos["side"].(string)
		if psym != symbol || strings.ToLower(pside) != side {
			continue
		}
		pa, _ := pos["positionAmt"].(float64)
		var abs float64
		if strings.ToLower(pside) == "short" && pa < 0 {
			abs = -pa
		} else {
			abs = pa
		}
		return math.Floor(abs*1e8) / 1e8
	}
	return 0
}

// getPositionSizeFromREST fetches position from fapi/v2/positionRisk for final reconciliation when cache=0
func (t *FuturesTrader) getPositionSizeFromREST(symbol, side string) float64 {
	if err := CheckCircuitBreaker(); err != nil {
		return 0
	}
	positions, err := t.client.NewGetPositionRiskService().Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return 0
	}
	for _, pos := range positions {
		posAmt, _ := strconv.ParseFloat(pos.PositionAmt, 64)
		if posAmt == 0 {
			continue
		}
		pside := "long"
		if posAmt < 0 {
			pside = "short"
		}
		if pos.Symbol != symbol || pside != side {
			continue
		}
		abs := posAmt
		if abs < 0 {
			abs = -abs
		}
		return math.Floor(abs*1e8) / 1e8
	}
	return 0
}

// formatQuantityFloor formats quantity with Floor to never exceed position (avoids -2022)
func (t *FuturesTrader) formatQuantityFloor(symbol string, quantity float64) (string, error) {
	precision, err := t.GetSymbolPrecision(symbol)
	if err != nil {
		return fmt.Sprintf("%.3f", quantity), nil
	}
	mult := math.Pow(10, float64(precision))
	floored := math.Floor(quantity*mult) / mult
	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, floored), nil
}

// CloseLong closes a long position. side=SELL, positionSide=LONG. Hedge mode: do NOT send reduceOnly.
func (t *FuturesTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	t.ensureTimeSync()
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel pending orders before close: %v", err)
	}

	// 平仓前终极 REST 校验：忽略缓存，强制调用 fapi/v2/positionRisk 获取真实仓位
	currentSize := t.getPositionSizeFromREST(symbol, "long")
	if currentSize <= 0 {
		logger.Infof("[INFO] Position already closed, skipping. symbol=%s side=LONG", symbol)
		return map[string]interface{}{"orderId": int64(0), "status": "ALREADY_CLOSED", "alreadyClosed": true}, nil
	}

	if quantity == 0 {
		quantity = currentSize
	}
	if quantity > currentSize {
		quantity = currentSize
	}

	quantityStr, err := t.formatQuantityFloor(symbol, quantity)
	if err != nil {
		return nil, err
	}
	quantityFloat, _ := strconv.ParseFloat(quantityStr, 64)
	if quantityFloat <= 0 {
		return nil, fmt.Errorf("formatted quantity too small: %s", quantityStr)
	}
	if quantityFloat > currentSize {
		quantityStr, _ = t.formatQuantityFloor(symbol, currentSize)
	}

	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeSell).
		PositionSide(futures.PositionSideTypeLong).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		SetCircuitBreakerFromError(err)
		if strings.Contains(err.Error(), "-2022") || strings.Contains(err.Error(), "2022") {
			logger.Errorf("[Binance -2022] symbol=%s side=SELL positionSide=LONG quantity=%s actualPositionSize=%.8f err=%v",
				symbol, quantityStr, currentSize, err)
		}
		return nil, fmt.Errorf("failed to close long position: %w", err)
	}

	logger.Infof("✓ Closed long position successfully: %s quantity: %s", symbol, quantityStr)

	// 脏写缓存：不等 WebSocket，立即将该 symbol LONG 仓位标记为 0
	t.ForceZeroPositionInCache(symbol, "LONG")

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// CloseShort closes a short position. side=BUY, positionSide=SHORT. Hedge mode: do NOT send reduceOnly.
func (t *FuturesTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	t.ensureTimeSync()
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel pending orders before close: %v", err)
	}

	// 平仓前终极 REST 校验：忽略缓存，强制调用 fapi/v2/positionRisk 获取真实仓位
	currentSize := t.getPositionSizeFromREST(symbol, "short")
	if currentSize <= 0 {
		logger.Infof("[INFO] Position already closed, skipping. symbol=%s side=SHORT", symbol)
		return map[string]interface{}{"orderId": int64(0), "status": "ALREADY_CLOSED", "alreadyClosed": true}, nil
	}

	if quantity == 0 {
		quantity = currentSize
	}
	if quantity > currentSize {
		quantity = currentSize
	}

	quantityStr, err := t.formatQuantityFloor(symbol, quantity)
	if err != nil {
		return nil, err
	}
	quantityFloat, _ := strconv.ParseFloat(quantityStr, 64)
	if quantityFloat <= 0 {
		return nil, fmt.Errorf("formatted quantity too small: %s", quantityStr)
	}
	if quantityFloat > currentSize {
		quantityStr, _ = t.formatQuantityFloor(symbol, currentSize)
	}

	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeBuy).
		PositionSide(futures.PositionSideTypeShort).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		SetCircuitBreakerFromError(err)
		if strings.Contains(err.Error(), "-2022") || strings.Contains(err.Error(), "2022") {
			logger.Errorf("[Binance -2022] symbol=%s side=BUY positionSide=SHORT quantity=%s actualPositionSize=%.8f err=%v",
				symbol, quantityStr, currentSize, err)
		}
		return nil, fmt.Errorf("failed to close short position: %w", err)
	}

	logger.Infof("✓ Closed short position successfully: %s quantity: %s", symbol, quantityStr)

	// 脏写缓存：不等 WebSocket，立即将该 symbol SHORT 仓位标记为 0
	t.ForceZeroPositionInCache(symbol, "SHORT")

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// CancelStopLossOrders cancels only stop-loss orders (doesn't affect take-profit orders)
// Now uses both legacy API and new Algo Order API
func (t *FuturesTrader) CancelStopLossOrders(symbol string) error {
	if err := CheckCircuitBreaker(); err != nil {
		return err
	}
	canceledCount := 0
	var cancelErrors []error

	// 1. Cancel legacy stop-loss orders
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, order := range orders {
			orderType := string(order.Type)

			// Only cancel stop-loss orders (don't cancel take-profit orders)
			// Use string comparison since OrderType constants were removed in v2.8.9
			if orderType == "STOP_MARKET" || orderType == "STOP" {
				_, err := t.client.NewCancelOrderService().
					Symbol(symbol).
					OrderID(order.OrderID).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Order ID %d: %v", order.OrderID, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel legacy stop-loss order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled legacy stop-loss order (Order ID: %d, Type: %s, Side: %s)", order.OrderID, orderType, order.PositionSide)
			}
		}
	}

	// 2. Cancel Algo stop-loss orders
	algoOrders, err := t.client.NewListOpenAlgoOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, algoOrder := range algoOrders {
			// Only cancel stop-loss orders
			if algoOrder.OrderType == futures.AlgoOrderTypeStopMarket || algoOrder.OrderType == futures.AlgoOrderTypeStop {
				_, err := t.client.NewCancelAlgoOrderService().
					AlgoID(algoOrder.AlgoId).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Algo ID %d: %v", algoOrder.AlgoId, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel Algo stop-loss order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled Algo stop-loss order (Algo ID: %d, Type: %s)", algoOrder.AlgoId, algoOrder.OrderType)
			}
		}
	}

	if canceledCount == 0 && len(cancelErrors) == 0 {
		logger.Infof("  ℹ %s has no stop-loss orders to cancel", symbol)
	} else if canceledCount > 0 {
		logger.Infof("  ✓ Canceled %d stop-loss order(s) for %s", canceledCount, symbol)
	}

	// If all cancellations failed, return error
	if len(cancelErrors) > 0 && canceledCount == 0 {
		return fmt.Errorf("failed to cancel stop-loss orders: %v", cancelErrors)
	}

	return nil
}

// CancelTakeProfitOrders cancels only take-profit orders (doesn't affect stop-loss orders)
// Now uses both legacy API and new Algo Order API
func (t *FuturesTrader) CancelTakeProfitOrders(symbol string) error {
	if err := CheckCircuitBreaker(); err != nil {
		return err
	}
	canceledCount := 0
	var cancelErrors []error

	// 1. Cancel legacy take-profit orders
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, order := range orders {
			orderType := string(order.Type)

			// Only cancel take-profit orders (don't cancel stop-loss orders)
			// Use string comparison since OrderType constants were removed in v2.8.9
			if orderType == "TAKE_PROFIT_MARKET" || orderType == "TAKE_PROFIT" {
				_, err := t.client.NewCancelOrderService().
					Symbol(symbol).
					OrderID(order.OrderID).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Order ID %d: %v", order.OrderID, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel legacy take-profit order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled legacy take-profit order (Order ID: %d, Type: %s, Side: %s)", order.OrderID, orderType, order.PositionSide)
			}
		}
	}

	// 2. Cancel Algo take-profit orders
	algoOrders, err := t.client.NewListOpenAlgoOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, algoOrder := range algoOrders {
			// Only cancel take-profit orders
			if algoOrder.OrderType == futures.AlgoOrderTypeTakeProfitMarket || algoOrder.OrderType == futures.AlgoOrderTypeTakeProfit {
				_, err := t.client.NewCancelAlgoOrderService().
					AlgoID(algoOrder.AlgoId).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Algo ID %d: %v", algoOrder.AlgoId, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel Algo take-profit order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled Algo take-profit order (Algo ID: %d, Type: %s)", algoOrder.AlgoId, algoOrder.OrderType)
			}
		}
	}

	if canceledCount == 0 && len(cancelErrors) == 0 {
		logger.Infof("  ℹ %s has no take-profit orders to cancel", symbol)
	} else if canceledCount > 0 {
		logger.Infof("  ✓ Canceled %d take-profit order(s) for %s", canceledCount, symbol)
	}

	// If all cancellations failed, return error
	if len(cancelErrors) > 0 && canceledCount == 0 {
		return fmt.Errorf("failed to cancel take-profit orders: %v", cancelErrors)
	}

	return nil
}

// CancelAllOrders cancels all pending orders for this symbol
// Now uses both legacy API and new Algo Order API
func (t *FuturesTrader) CancelAllOrders(symbol string) error {
	if err := CheckCircuitBreaker(); err != nil {
		return err
	}
	// 1. Cancel all legacy orders
	err := t.client.NewCancelAllOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		logger.Infof("  ⚠ Failed to cancel legacy orders: %v", err)
	} else {
		logger.Infof("  ✓ Canceled all legacy pending orders for %s", symbol)
	}

	// 2. Cancel all Algo orders
	err = t.client.NewCancelAllAlgoOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		// Ignore "no algo orders" error
		if !contains(err.Error(), "no algo") && !contains(err.Error(), "No algo") {
			logger.Infof("  ⚠ Failed to cancel Algo orders: %v", err)
		}
	} else {
		logger.Infof("  ✓ Canceled all Algo orders for %s", symbol)
	}

	return nil
}

// PlaceLimitOrder places a limit order for grid trading
// This implements the GridTrader interface for FuturesTrader
func (t *FuturesTrader) PlaceLimitOrder(req *types.LimitOrderRequest) (*types.LimitOrderResult, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	// Format quantity to correct precision
	quantityStr, err := t.FormatQuantity(req.Symbol, req.Quantity)
	if err != nil {
		return nil, fmt.Errorf("failed to format quantity: %w", err)
	}

	// Format price to correct precision
	priceStr, err := t.FormatPrice(req.Symbol, req.Price)
	if err != nil {
		return nil, fmt.Errorf("failed to format price: %w", err)
	}

	// Set leverage if specified
	if req.Leverage > 0 {
		if err := t.SetLeverage(req.Symbol, req.Leverage); err != nil {
			logger.Warnf("Failed to set leverage: %v", err)
		}
	}

	// Determine side and position side
	var side futures.SideType
	var positionSide futures.PositionSideType

	if req.Side == "BUY" {
		side = futures.SideTypeBuy
	} else {
		side = futures.SideTypeSell
	}

	switch strings.ToUpper(strings.TrimSpace(req.PositionSide)) {
	case "SHORT":
		positionSide = futures.PositionSideTypeShort
	default:
		positionSide = futures.PositionSideTypeLong
	}

	// Build order service with broker ID
	orderService := t.client.NewCreateOrderService().
		Symbol(req.Symbol).
		Side(side).
		PositionSide(positionSide).
		Type(futures.OrderTypeLimit).
		TimeInForce(futures.TimeInForceTypeGTC).
		Quantity(quantityStr).
		Price(priceStr).
		NewClientOrderID(getBrOrderID())

	if req.ReduceOnly {
		orderService = orderService.ReduceOnly(true)
	}

	// Execute order
	order, err := orderService.Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to place limit order: %w", err)
	}

	logger.Infof("✓ [Grid] Placed limit order: %s %s %s @ %s, qty=%s, orderID=%d",
		req.Symbol, req.Side, positionSide, priceStr, quantityStr, order.OrderID)

	return &types.LimitOrderResult{
		OrderID:      fmt.Sprintf("%d", order.OrderID),
		ClientID:     order.ClientOrderID,
		Symbol:       order.Symbol,
		Side:         string(order.Side),
		PositionSide: string(order.PositionSide),
		Price:        req.Price,
		Quantity:     req.Quantity,
		Status:       string(order.Status),
	}, nil
}

// CancelOrder cancels a specific order by ID
// This implements the GridTrader interface for FuturesTrader
func (t *FuturesTrader) CancelOrder(symbol, orderID string) error {
	if err := CheckCircuitBreaker(); err != nil {
		return err
	}
	// Parse order ID to int64
	orderIDInt, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid order ID: %w", err)
	}

	_, err = t.client.NewCancelOrderService().
		Symbol(symbol).
		OrderID(orderIDInt).
		Do(context.Background())

	if err != nil {
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	logger.Infof("✓ [Grid] Cancelled order: %s/%s", symbol, orderID)
	return nil
}

// GetOrderBook gets the order book for a symbol
// This implements the GridTrader interface for FuturesTrader
func (t *FuturesTrader) GetOrderBook(symbol string, depth int) (bids, asks [][]float64, err error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, nil, err
	}
	book, err := t.client.NewDepthService().
		Symbol(symbol).
		Limit(depth).
		Do(context.Background())

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get order book: %w", err)
	}

	// Convert bids
	bids = make([][]float64, len(book.Bids))
	for i, bid := range book.Bids {
		price, _ := strconv.ParseFloat(bid.Price, 64)
		qty, _ := strconv.ParseFloat(bid.Quantity, 64)
		bids[i] = []float64{price, qty}
	}

	// Convert asks
	asks = make([][]float64, len(book.Asks))
	for i, ask := range book.Asks {
		price, _ := strconv.ParseFloat(ask.Price, 64)
		qty, _ := strconv.ParseFloat(ask.Quantity, 64)
		asks[i] = []float64{price, qty}
	}

	return bids, asks, nil
}

// CancelStopOrders cancels take-profit/stop-loss orders for this symbol (used to adjust TP/SL positions)
// Now uses both legacy API and new Algo Order API (Binance migrated stop orders to Algo system)
func (t *FuturesTrader) CancelStopOrders(symbol string) error {
	if err := CheckCircuitBreaker(); err != nil {
		return err
	}
	canceledCount := 0

	// 1. Cancel legacy stop orders (for backward compatibility)
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, order := range orders {
			orderType := string(order.Type)

			// Only cancel stop-loss and take-profit orders
			// Use string comparison since OrderType constants were removed in v2.8.9
			if orderType == "STOP_MARKET" ||
				orderType == "TAKE_PROFIT_MARKET" ||
				orderType == "STOP" ||
				orderType == "TAKE_PROFIT" {

				_, err := t.client.NewCancelOrderService().
					Symbol(symbol).
					OrderID(order.OrderID).
					Do(context.Background())

				if err != nil {
					logger.Infof("  ⚠ Failed to cancel legacy order %d: %v", order.OrderID, err)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled legacy stop order for %s (Order ID: %d, Type: %s)",
					symbol, order.OrderID, orderType)
			}
		}
	}

	// 2. Cancel Algo orders (new API)
	err = t.client.NewCancelAllAlgoOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		// Ignore "no algo orders" error
		if !contains(err.Error(), "no algo") && !contains(err.Error(), "No algo") {
			logger.Infof("  ⚠ Failed to cancel Algo orders: %v", err)
		}
	} else {
		logger.Infof("  ✓ Canceled all Algo orders for %s", symbol)
		canceledCount++
	}

	if canceledCount == 0 {
		logger.Infof("  ℹ %s has no take-profit/stop-loss orders to cancel", symbol)
	}

	return nil
}

// GetOpenOrders gets all open/pending orders for a symbol
func (t *FuturesTrader) GetOpenOrders(symbol string) ([]types.OpenOrder, error) {
	var result []types.OpenOrder

	// 1. Get legacy open orders
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("failed to get open orders: %w", err)
	}

	for _, order := range orders {
		price, _ := strconv.ParseFloat(order.Price, 64)
		stopPrice, _ := strconv.ParseFloat(order.StopPrice, 64)
		quantity, _ := strconv.ParseFloat(order.OrigQuantity, 64)

		result = append(result, types.OpenOrder{
			OrderID:      fmt.Sprintf("%d", order.OrderID),
			Symbol:       order.Symbol,
			Side:         string(order.Side),
			PositionSide: string(order.PositionSide),
			Type:         string(order.Type),
			Price:        price,
			StopPrice:    stopPrice,
			Quantity:     quantity,
			Status:       string(order.Status),
		})
	}

	// 2. Get Algo orders (new API for stop-loss/take-profit)
	algoOrders, err := t.client.NewListOpenAlgoOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, algoOrder := range algoOrders {
			triggerPrice, _ := strconv.ParseFloat(algoOrder.TriggerPrice, 64)
			quantity, _ := strconv.ParseFloat(algoOrder.Quantity, 64)

			result = append(result, types.OpenOrder{
				OrderID:      fmt.Sprintf("%d", algoOrder.AlgoId),
				Symbol:       algoOrder.Symbol,
				Side:         string(algoOrder.Side),
				PositionSide: string(algoOrder.PositionSide),
				Type:         string(algoOrder.OrderType),
				Price:        0, // Algo orders use stop price
				StopPrice:    triggerPrice,
				Quantity:     quantity,
				Status:       "NEW",
			})
		}
	}

	return result, nil
}

// GetMarketPrice gets market price
func (t *FuturesTrader) GetMarketPrice(symbol string) (float64, error) {
	prices, err := t.client.NewListPricesService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get price: %w", err)
	}

	if len(prices) == 0 {
		return 0, fmt.Errorf("price not found")
	}

	price, err := strconv.ParseFloat(prices[0].Price, 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

// CalculatePositionSize calculates position size
func (t *FuturesTrader) CalculatePositionSize(balance, riskPercent, price float64, leverage int) float64 {
	riskAmount := balance * (riskPercent / 100.0)
	positionValue := riskAmount * float64(leverage)
	quantity := positionValue / price
	return quantity
}

// SetStopLoss sets stop-loss order using new Algo Order API
// Binance has migrated stop orders to Algo Order system (error -4120 STOP_ORDER_SWITCH_ALGO)
func (t *FuturesTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	// Use new Algo Order API
	_, err := t.client.NewCreateAlgoOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.AlgoOrderTypeStopMarket).
		TriggerPrice(fmt.Sprintf("%.8f", stopPrice)).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true).
		ClientAlgoId(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		if strings.Contains(err.Error(), "-4509") || strings.Contains(err.Error(), "positions are available") {
			logger.Infof("  ⚠ Binance reports no position for stop-loss, clearing cache: %s %s", symbol, positionSide)
			t.ForceZeroPositionInCache(symbol, positionSide)
		}
		return fmt.Errorf("failed to set stop-loss: %w", err)
	}

	logger.Infof("  Stop-loss price set (Algo Order): %.4f", stopPrice)
	return nil
}

// SetTakeProfit sets take-profit order using new Algo Order API
// Binance has migrated stop orders to Algo Order system (error -4120 STOP_ORDER_SWITCH_ALGO)
func (t *FuturesTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	// Use new Algo Order API
	_, err := t.client.NewCreateAlgoOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.AlgoOrderTypeTakeProfitMarket).
		TriggerPrice(fmt.Sprintf("%.8f", takeProfitPrice)).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true).
		ClientAlgoId(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		if strings.Contains(err.Error(), "-4509") || strings.Contains(err.Error(), "positions are available") {
			logger.Infof("  ⚠ Binance reports no position for take-profit, clearing cache: %s %s", symbol, positionSide)
			t.ForceZeroPositionInCache(symbol, positionSide)
		}
		return fmt.Errorf("failed to set take-profit: %w", err)
	}

	logger.Infof("  Take-profit price set (Algo Order): %.4f", takeProfitPrice)
	return nil
}

// GetMinNotional gets minimum notional value (Binance requirement)
func (t *FuturesTrader) GetMinNotional(symbol string) float64 {
	// Use conservative default value of 10 USDT to ensure order passes exchange validation
	return 10.0
}

// CheckMinNotional checks if order meets minimum notional value requirement
func (t *FuturesTrader) CheckMinNotional(symbol string, quantity float64) error {
	price, err := t.GetMarketPrice(symbol)
	if err != nil {
		return fmt.Errorf("failed to get market price: %w", err)
	}

	notionalValue := quantity * price
	minNotional := t.GetMinNotional(symbol)

	if notionalValue < minNotional {
		return fmt.Errorf(
			"order amount %.2f USDT is below minimum requirement %.2f USDT (quantity: %.4f, price: %.4f)",
			notionalValue, minNotional, quantity, price,
		)
	}

	return nil
}

// getSymbolLotSize returns LOT_SIZE filter: minQty, maxQty, stepSize (float64), precision. err != nil or maxQty==0 means not found.
func (t *FuturesTrader) getSymbolLotSize(symbol string) (minQty, maxQty, stepSize float64, precision int, err error) {
	exchangeInfo, err := t.client.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("failed to get trading rules: %w", err)
	}

	for _, s := range exchangeInfo.Symbols {
		if s.Symbol != symbol {
			continue
		}
		for _, filter := range s.Filters {
			if filter["filterType"] != "LOT_SIZE" {
				continue
			}
			minStr, _ := filter["minQty"].(string)
			maxStr, _ := filter["maxQty"].(string)
			stepStr, _ := filter["stepSize"].(string)
			minQty, _ = strconv.ParseFloat(minStr, 64)
			maxQty, _ = strconv.ParseFloat(maxStr, 64)
			stepSize, _ = strconv.ParseFloat(stepStr, 64)
			if stepSize <= 0 {
				stepSize = 0.001
			}
			precision = calculatePrecision(stepStr)
			return minQty, maxQty, stepSize, precision, nil
		}
		break
	}

	return 0, 0, 0, 3, nil // symbol not found, caller will use defaults
}

// GetSymbolPrecision gets the quantity precision for a trading pair
func (t *FuturesTrader) GetSymbolPrecision(symbol string) (int, error) {
	_, _, _, precision, err := t.getSymbolLotSize(symbol)
	if err != nil {
		return 0, err
	}
	if precision == 0 && err == nil {
		logger.Infof("  ⚠ %s precision information not found, using default precision 3", symbol)
		return 3, nil
	}
	return precision, nil
}

// calculatePrecision calculates precision from stepSize
func calculatePrecision(stepSize string) int {
	// Remove trailing zeros
	stepSize = trimTrailingZeros(stepSize)

	// Find decimal point
	dotIndex := -1
	for i := 0; i < len(stepSize); i++ {
		if stepSize[i] == '.' {
			dotIndex = i
			break
		}
	}

	// If no decimal point or decimal point is at the end, precision is 0
	if dotIndex == -1 || dotIndex == len(stepSize)-1 {
		return 0
	}

	// Return number of digits after decimal point
	return len(stepSize) - dotIndex - 1
}

// trimTrailingZeros removes trailing zeros
func trimTrailingZeros(s string) string {
	// If no decimal point, return directly
	if !stringContains(s, ".") {
		return s
	}

	// Iterate backwards to remove trailing zeros
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}

	// If last character is decimal point, remove it too
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}

	return s
}

// FormatQuantity formats quantity to symbol's precision, caps to maxQty, and rounds down to stepSize (avoids -4005 Quantity greater than max quantity).
func (t *FuturesTrader) FormatQuantity(symbol string, quantity float64) (string, error) {
	minQty, maxQty, stepSize, precision, err := t.getSymbolLotSize(symbol)
	if err != nil {
		return fmt.Sprintf("%.3f", quantity), nil
	}

	q := quantity
	if maxQty > 0 && q > maxQty {
		logger.Infof("  ⚠ %s quantity %.8f exceeds exchange maxQty %.8f, capping to max", symbol, q, maxQty)
		q = maxQty
	}
	if stepSize > 0 {
		// Round down to step size to satisfy LOT_SIZE
		q = math.Floor(q/stepSize+1e-15) * stepSize
	}
	if minQty > 0 && q < minQty {
		q = 0
	}

	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, q), nil
}

// GetSymbolPricePrecision gets the price precision for a trading pair
func (t *FuturesTrader) GetSymbolPricePrecision(symbol string) (int, error) {
	exchangeInfo, err := t.client.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to get trading rules: %w", err)
	}

	for _, s := range exchangeInfo.Symbols {
		if s.Symbol == symbol {
			// Get precision from PRICE_FILTER filter
			for _, filter := range s.Filters {
				if filter["filterType"] == "PRICE_FILTER" {
					tickSize := filter["tickSize"].(string)
					precision := calculatePrecision(tickSize)
					return precision, nil
				}
			}
		}
	}

	// Default to 2 decimal places for price
	return 2, nil
}

// FormatPrice formats price to correct precision
func (t *FuturesTrader) FormatPrice(symbol string, price float64) (string, error) {
	precision, err := t.GetSymbolPricePrecision(symbol)
	if err != nil {
		// If retrieval fails, use default format
		return fmt.Sprintf("%.2f", price), nil
	}

	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, price), nil
}

// Helper functions
func contains(s, substr string) bool {
	return len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// GetOrderStatus gets order status
func (t *FuturesTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	// Convert orderID to int64
	orderIDInt, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid order ID: %s", orderID)
	}

	order, err := t.client.NewGetOrderService().
		Symbol(symbol).
		OrderID(orderIDInt).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get order status: %w", err)
	}

	// Parse execution price
	avgPrice, _ := strconv.ParseFloat(order.AvgPrice, 64)
	executedQty, _ := strconv.ParseFloat(order.ExecutedQuantity, 64)

	result := map[string]interface{}{
		"orderId":     order.OrderID,
		"symbol":      order.Symbol,
		"status":      string(order.Status),
		"avgPrice":    avgPrice,
		"executedQty": executedQty,
		"side":        string(order.Side),
		"type":        string(order.Type),
		"time":        order.Time,
		"updateTime":  order.UpdateTime,
	}

	// Binance futures commission fee needs to be obtained through GetUserTrades, not retrieved here for now
	// Can be obtained later through WebSocket or separate query
	result["commission"] = 0.0

	return result, nil
}

// GetClosedPnL retrieves recent closing trades from Binance Futures
// Note: Binance does NOT have a position history API, only trade history.
// This returns individual closing trades (realizedPnl != 0) for real-time position closure detection.
// NOT suitable for historical position reconstruction - use only for matching recent closures.
func (t *FuturesTrader) GetClosedPnL(startTime time.Time, limit int) ([]types.ClosedPnLRecord, error) {
	trades, err := t.GetTrades(startTime, limit)
	if err != nil {
		return nil, err
	}

	// Filter only closing trades (realizedPnl != 0) and convert to ClosedPnLRecord
	var records []types.ClosedPnLRecord
	for _, trade := range trades {
		if trade.RealizedPnL == 0 {
			continue // Skip opening trades
		}

		// Determine side from trade
		side := "long"
		if trade.PositionSide == "SHORT" || trade.PositionSide == "short" {
			side = "short"
		} else if trade.PositionSide == "BOTH" || trade.PositionSide == "" {
			// One-way mode: selling closes long, buying closes short
			if trade.Side == "SELL" || trade.Side == "Sell" {
				side = "long"
			} else {
				side = "short"
			}
		}

		// Calculate entry price from PnL (mathematically accurate for this trade)
		var entryPrice float64
		if trade.Quantity > 0 {
			if side == "long" {
				entryPrice = trade.Price - trade.RealizedPnL/trade.Quantity
			} else {
				entryPrice = trade.Price + trade.RealizedPnL/trade.Quantity
			}
		}

		records = append(records, types.ClosedPnLRecord{
			Symbol:      trade.Symbol,
			Side:        side,
			EntryPrice:  entryPrice,
			ExitPrice:   trade.Price,
			Quantity:    trade.Quantity,
			RealizedPnL: trade.RealizedPnL,
			Fee:         trade.Fee,
			ExitTime:    trade.Time,
			EntryTime:   trade.Time, // Approximate
			OrderID:     trade.TradeID,
			ExchangeID:  trade.TradeID,
			CloseType:   "unknown",
		})
	}

	return records, nil
}

// GetTrades retrieves trade history from Binance Futures using Income API
// Note: Income API has delays (~minutes), for real-time use GetTradesForSymbol instead
func (t *FuturesTrader) GetTrades(startTime time.Time, limit int) ([]types.TradeRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	// Use Income API to get REALIZED_PNL records (all symbols)
	incomes, err := t.client.NewGetIncomeHistoryService().
		IncomeType("REALIZED_PNL").
		StartTime(startTime.UnixMilli()).
		Limit(int64(limit)).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get income history: %w", err)
	}

	var trades []types.TradeRecord
	for _, income := range incomes {
		pnl, _ := strconv.ParseFloat(income.Income, 64)
		if pnl == 0 {
			continue // Skip zero PnL records
		}

		// Income API doesn't provide full trade details, create a minimal record
		// This is mainly used for detecting recent closures, not historical reconstruction
		trade := types.TradeRecord{
			TradeID:     strconv.FormatInt(income.TranID, 10),
			Symbol:      income.Symbol,
			RealizedPnL: pnl,
			Time:        time.UnixMilli(income.Time).UTC(),
			// Note: Income API doesn't provide price, quantity, side, fee
			// For accurate data, use GetTradesForSymbol with specific symbol
		}
		trades = append(trades, trade)
	}

	return trades, nil
}

// GetTradesForSymbol retrieves trade history for a specific symbol
// This is more reliable than using Income API which may have delays
func (t *FuturesTrader) GetTradesForSymbol(symbol string, startTime time.Time, limit int) ([]types.TradeRecord, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	accountTrades, err := t.client.NewListAccountTradeService().
		Symbol(symbol).
		StartTime(startTime.UnixMilli()).
		Limit(limit).
		Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return nil, fmt.Errorf("failed to get trade history for %s: %w", symbol, err)
	}

	var trades []types.TradeRecord
	for _, at := range accountTrades {
		price, _ := strconv.ParseFloat(at.Price, 64)
		qty, _ := strconv.ParseFloat(at.Quantity, 64)
		fee, _ := strconv.ParseFloat(at.Commission, 64)
		pnl, _ := strconv.ParseFloat(at.RealizedPnl, 64)

		trade := types.TradeRecord{
			TradeID:      strconv.FormatInt(at.ID, 10),
			OrderID:      strconv.FormatInt(at.OrderID, 10),
			Symbol:       at.Symbol,
			Side:         string(at.Side),
			PositionSide: string(at.PositionSide),
			Price:        price,
			Quantity:     qty,
			RealizedPnL:  pnl,
			Fee:          fee,
			Time:         time.UnixMilli(at.Time).UTC(),
		}
		trades = append(trades, trade)
	}

	return trades, nil
}

// GetTradesForSymbolFromID retrieves trade history for a specific symbol starting from a given trade ID
// This is used for incremental sync - only fetch new trades since last sync
func (t *FuturesTrader) GetTradesForSymbolFromID(symbol string, fromID int64, limit int) ([]types.TradeRecord, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	accountTrades, err := t.client.NewListAccountTradeService().
		Symbol(symbol).
		FromID(fromID).
		Limit(limit).
		Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return nil, fmt.Errorf("failed to get trade history for %s from ID %d: %w", symbol, fromID, err)
	}

	var trades []types.TradeRecord
	for _, at := range accountTrades {
		price, _ := strconv.ParseFloat(at.Price, 64)
		qty, _ := strconv.ParseFloat(at.Quantity, 64)
		fee, _ := strconv.ParseFloat(at.Commission, 64)
		pnl, _ := strconv.ParseFloat(at.RealizedPnl, 64)

		trade := types.TradeRecord{
			TradeID:      strconv.FormatInt(at.ID, 10),
			OrderID:      strconv.FormatInt(at.OrderID, 10),
			Symbol:       at.Symbol,
			Side:         string(at.Side),
			PositionSide: string(at.PositionSide),
			Price:        price,
			Quantity:     qty,
			RealizedPnL:  pnl,
			Fee:          fee,
			Time:         time.UnixMilli(at.Time).UTC(),
		}
		trades = append(trades, trade)
	}

	return trades, nil
}

// GetCommissionSymbols returns symbols that have new commission records since lastSyncTime
// COMMISSION income is generated for every trade, so this is more reliable than REALIZED_PNL
func (t *FuturesTrader) GetCommissionSymbols(lastSyncTime time.Time) ([]string, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	incomes, err := t.client.NewGetIncomeHistoryService().
		IncomeType("COMMISSION").
		StartTime(lastSyncTime.UnixMilli()).
		Limit(1000).
		Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return nil, fmt.Errorf("failed to get commission history: %w", err)
	}

	symbolMap := make(map[string]bool)
	for _, income := range incomes {
		if income.Symbol != "" {
			symbolMap[income.Symbol] = true
		}
	}

	var symbols []string
	for symbol := range symbolMap {
		symbols = append(symbols, symbol)
	}

	return symbols, nil
}

// GetPnLSymbols returns symbols that have REALIZED_PNL records since lastSyncTime
// This is a fallback when COMMISSION detection fails (VIP users, BNB fee discount)
func (t *FuturesTrader) GetPnLSymbols(lastSyncTime time.Time) ([]string, error) {
	if err := CheckCircuitBreaker(); err != nil {
		return nil, err
	}
	incomes, err := t.client.NewGetIncomeHistoryService().
		IncomeType("REALIZED_PNL").
		StartTime(lastSyncTime.UnixMilli()).
		Limit(1000).
		Do(context.Background())
	if err != nil {
		SetCircuitBreakerFromError(err)
		return nil, fmt.Errorf("failed to get PnL history: %w", err)
	}

	symbolMap := make(map[string]bool)
	for _, income := range incomes {
		if income.Symbol != "" {
			symbolMap[income.Symbol] = true
		}
	}

	var symbols []string
	for symbol := range symbolMap {
		symbols = append(symbols, symbol)
	}

	return symbols, nil
}
