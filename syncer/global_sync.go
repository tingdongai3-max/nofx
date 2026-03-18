package syncer

import (
	"fmt"
	"nofx/logger"
	"nofx/store"
	"sync"
	"time"
)

const (
	minOrderSyncInterval  = 2 * time.Minute
	balanceCacheMaxAge    = 5 * time.Minute
	positionCacheMaxAge   = 5 * time.Second
)

// GlobalSyncManager 全局同步管理器（单例模式）
// 同一交易所 API 账号全局只运行一个 OrderSync 协程，负责拉取成交记录并分发
type GlobalSyncManager struct {
	mu sync.RWMutex

	// 每个交易所的同步状态
	syncers map[string]*exchangeSyncer // key: exchangeID (UUID)

	// 全局余额缓存
	balanceCache map[string]*balanceCacheEntry // key: accountKey

	// 全局仓位缓存
	positionCache map[string]*positionCacheEntry // key: accountKey
}

type exchangeSyncer struct {
	exchangeID   string
	exchangeType string
	store        *store.Store
	interval     time.Duration

	// 停止控制
	stopChan chan struct{}

	// 订阅的交易员列表
	subscribers map[string]struct{} // key: traderID

	// 具体同步逻辑（由调用方注入）
	syncFn func() error
}

type balanceCacheEntry struct {
	balance   map[string]interface{}
	timestamp time.Time
}

type positionCacheEntry struct {
	positions []map[string]interface{}
	timestamp time.Time
}

// 全局同步管理器实例
var (
	globalSyncManager     *GlobalSyncManager
	globalSyncManagerOnce sync.Once
)

// GetGlobalSyncManager 获取全局同步管理器（单例）
func GetGlobalSyncManager() *GlobalSyncManager {
	globalSyncManagerOnce.Do(func() {
		globalSyncManager = &GlobalSyncManager{
			syncers:       make(map[string]*exchangeSyncer),
			balanceCache:  make(map[string]*balanceCacheEntry),
			positionCache: make(map[string]*positionCacheEntry),
		}
		logger.Infof("🌐 GlobalSyncManager initialized")
	})
	return globalSyncManager
}

// StartExchangeSync 启动指定交易所的全局同步（单例模式）
// 同一交易所只允许运行一个同步协程
func (gsm *GlobalSyncManager) StartExchangeSync(exchangeID, exchangeType string, st *store.Store, interval time.Duration, syncFn func() error) error {
	if exchangeID == "" {
		return fmt.Errorf("exchangeID is empty")
	}
	if interval < minOrderSyncInterval {
		interval = minOrderSyncInterval
	}

	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	// 检查是否已存在
	if syncer, exists := gsm.syncers[exchangeID]; exists {
		if syncer.syncFn == nil && syncFn != nil {
			syncer.syncFn = syncFn
		}
		logger.Infof("🔄 Exchange sync already running for %s (%s)", exchangeID, exchangeType)
		return nil
	}

	// 创建新的同步器
	syncer := &exchangeSyncer{
		exchangeID:   exchangeID,
		exchangeType: exchangeType,
		store:        st,
		interval:     interval,
		stopChan:     make(chan struct{}),
		subscribers:  make(map[string]struct{}),
		syncFn:       syncFn,
	}

	gsm.syncers[exchangeID] = syncer
	logger.Infof("🌐 Starting global exchange sync for %s (%s)", exchangeID, exchangeType)

	// 启动同步协程
	go gsm.runExchangeSync(syncer)

	return nil
}

// SubscribeTrader 订阅交易员的同步
func (gsm *GlobalSyncManager) SubscribeTrader(exchangeID, traderID string) {
	if exchangeID == "" || traderID == "" {
		return
	}
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	if syncer, exists := gsm.syncers[exchangeID]; exists {
		syncer.subscribers[traderID] = struct{}{}
		logger.Infof("📡 Trader %s subscribed to exchange %s sync", traderID, exchangeID)
	}
}

// UnsubscribeTrader 取消订阅
func (gsm *GlobalSyncManager) UnsubscribeTrader(exchangeID, traderID string) {
	if exchangeID == "" || traderID == "" {
		return
	}
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	if syncer, exists := gsm.syncers[exchangeID]; exists {
		delete(syncer.subscribers, traderID)
		logger.Infof("📡 Trader %s unsubscribed from exchange %s sync", traderID, exchangeID)

		// 如果没有订阅者了，停止同步
		if len(syncer.subscribers) == 0 {
			close(syncer.stopChan)
			delete(gsm.syncers, exchangeID)
			logger.Infof("🛑 Stopped global sync for exchange %s (no subscribers)", exchangeID)
		}
	}
}

// StopExchangeSync 停止指定交易所的同步
func (gsm *GlobalSyncManager) StopExchangeSync(exchangeID string) {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	if syncer, exists := gsm.syncers[exchangeID]; exists {
		close(syncer.stopChan)
		delete(gsm.syncers, exchangeID)
		logger.Infof("🛑 Stopped global sync for exchange %s", exchangeID)
	}
}

// GetBalance 获取余额（带缓存）
// 仅在缓存有效期内返回，避免频繁直连 API
func (gsm *GlobalSyncManager) GetBalance(accountKey string) (map[string]interface{}, bool) {
	gsm.mu.RLock()
	entry, exists := gsm.balanceCache[accountKey]
	gsm.mu.RUnlock()

	if !exists || time.Since(entry.timestamp) > balanceCacheMaxAge {
		return nil, false
	}

	// Return a copy to avoid races
	result := make(map[string]interface{}, len(entry.balance))
	for k, v := range entry.balance {
		result[k] = v
	}
	return result, true
}

// SetBalance 设置余额缓存
func (gsm *GlobalSyncManager) SetBalance(accountKey string, balance map[string]interface{}) {
	if accountKey == "" || balance == nil {
		return
	}
	gsm.mu.Lock()
	defer gsm.mu.Unlock()

	// Copy to avoid external mutation
	copied := make(map[string]interface{}, len(balance))
	for k, v := range balance {
		copied[k] = v
	}
	gsm.balanceCache[accountKey] = &balanceCacheEntry{
		balance:   copied,
		timestamp: time.Now(),
	}
}

// GetPositions 获取仓位缓存。
// maxAge<=0 时默认仅信任最近 5 秒数据，避免多个组件重复打仓位 REST。
func (gsm *GlobalSyncManager) GetPositions(accountKey string, maxAge time.Duration) ([]map[string]interface{}, bool) {
	if maxAge <= 0 {
		maxAge = positionCacheMaxAge
	}

	gsm.mu.RLock()
	entry, exists := gsm.positionCache[accountKey]
	gsm.mu.RUnlock()

	if !exists || time.Since(entry.timestamp) > maxAge {
		return nil, false
	}

	result := make([]map[string]interface{}, len(entry.positions))
	for i, pos := range entry.positions {
		copied := make(map[string]interface{}, len(pos))
		for k, v := range pos {
			copied[k] = v
		}
		result[i] = copied
	}
	return result, true
}

// SetPositions 设置仓位缓存。
func (gsm *GlobalSyncManager) SetPositions(accountKey string, positions []map[string]interface{}) {
	if accountKey == "" || positions == nil {
		return
	}

	copied := make([]map[string]interface{}, len(positions))
	for i, pos := range positions {
		posCopy := make(map[string]interface{}, len(pos))
		for k, v := range pos {
			posCopy[k] = v
		}
		copied[i] = posCopy
	}

	gsm.mu.Lock()
	gsm.positionCache[accountKey] = &positionCacheEntry{
		positions: copied,
		timestamp: time.Now(),
	}
	gsm.mu.Unlock()
}

// ClearBalance removes cached balance for an exchange account.
func (gsm *GlobalSyncManager) ClearBalance(accountKey string) {
	if accountKey == "" {
		return
	}
	gsm.mu.Lock()
	delete(gsm.balanceCache, accountKey)
	gsm.mu.Unlock()
}

// ClearPositions removes cached positions for an exchange account.
func (gsm *GlobalSyncManager) ClearPositions(accountKey string) {
	if accountKey == "" {
		return
	}
	gsm.mu.Lock()
	delete(gsm.positionCache, accountKey)
	gsm.mu.Unlock()
}

// runExchangeSync 运行交易所同步协程
func (gsm *GlobalSyncManager) runExchangeSync(syncer *exchangeSyncer) {
	logger.Infof("🌐 Global sync started for exchange %s (interval: %v)", syncer.exchangeID, syncer.interval)

	// 初次同步立即执行一次
	gsm.syncExchange(syncer)

	ticker := time.NewTicker(syncer.interval)
	defer ticker.Stop()

	for {
		select {
		case <-syncer.stopChan:
			logger.Infof("🛑 Global sync stopped for exchange %s", syncer.exchangeID)
			return
		case <-ticker.C:
			gsm.syncExchange(syncer)
		}
	}
}

// syncExchange 执行交易所同步
func (gsm *GlobalSyncManager) syncExchange(syncer *exchangeSyncer) {
	if syncer.syncFn == nil {
		logger.Infof("⚠️ Global sync function not set for exchange %s", syncer.exchangeID)
		return
	}
	logger.Infof("🔄 Global sync running for exchange %s, %d subscribers",
		syncer.exchangeID, len(syncer.subscribers))
	if err := syncer.syncFn(); err != nil {
		logger.Infof("⚠️ Global sync failed for exchange %s: %v", syncer.exchangeID, err)
	}
}
