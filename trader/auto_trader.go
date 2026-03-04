package trader

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"nofx/experience"
	"nofx/kernel"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
	"nofx/trader/aster"
	"nofx/trader/binance"
	"nofx/trader/bitget"
	"nofx/trader/bybit"
	"nofx/trader/gate"
	"nofx/trader/hyperliquid"
	"nofx/trader/kucoin"
	"nofx/trader/lighter"
	"nofx/trader/okx"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AutoTraderConfig auto trading configuration (simplified version - AI makes all decisions)
type AutoTraderConfig struct {
	// Trader identification
	ID      string // Trader unique identifier (for log directory, etc.)
	Name    string // Trader display name
	AIModel string // AI model: "qwen" or "deepseek"

	// Trading platform selection
	Exchange   string // Exchange type: "binance", "bybit", "okx", "bitget", "gate", "hyperliquid", "aster" or "lighter"
	ExchangeID string // Exchange account UUID (for multi-account support)

	// Binance API configuration
	BinanceAPIKey     string
	BinanceSecretKey  string
	BinanceTestnet    bool   // true = 使用 testnet.binancefuture.com 模拟盘

	// Bybit API configuration
	BybitAPIKey    string
	BybitSecretKey string

	// OKX API configuration
	OKXAPIKey      string
	OKXSecretKey   string
	OKXPassphrase  string
	OKXTestnet     bool   // true = 模拟盘，请求头 x-simulated-trading: 1

	// Bitget API configuration
	BitgetAPIKey    string
	BitgetSecretKey string
	BitgetPassphrase string

	// Gate API configuration
	GateAPIKey    string
	GateSecretKey string

	// KuCoin API configuration
	KuCoinAPIKey    string
	KuCoinSecretKey string
	KuCoinPassphrase string

	// Hyperliquid configuration
	HyperliquidPrivateKey string
	HyperliquidWalletAddr string
	HyperliquidTestnet    bool

	// Aster configuration
	AsterUser       string // Aster main wallet address
	AsterSigner     string // Aster API wallet address
	AsterPrivateKey string // Aster API wallet private key

	// LIGHTER configuration
	LighterWalletAddr       string // LIGHTER wallet address (L1 wallet)
	LighterPrivateKey       string // LIGHTER L1 private key (for account identification)
	LighterAPIKeyPrivateKey string // LIGHTER API Key private key (40 bytes, for transaction signing)
	LighterAPIKeyIndex      int    // LIGHTER API Key index (0-255)
	LighterTestnet          bool   // Whether to use testnet

	// AI configuration
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// Custom AI API configuration
	CustomAPIURL    string
	CustomAPIKey    string
	CustomModelName string

	// Scan configuration
	ScanInterval time.Duration // Scan interval (recommended 3 minutes)

	// Account configuration
	InitialBalance float64 // Initial balance (for P&L calculation, must be set manually)

	// Dry-run (paper trading): no real orders; use VirtualEquity in Prompt and local ProcessTrade only
	IsDryRun      bool    // 模拟盘开关，默认 false
	VirtualEquity float64 // 模拟盘本金 USDT，IsDryRun 时传给 AI 的 Equity

	// Risk control (only as hints, AI can make autonomous decisions)
	MaxDailyLoss    float64       // Maximum daily loss percentage (hint)
	MaxDrawdown     float64       // Maximum drawdown percentage (hint)
	StopTradingTime time.Duration // Pause duration after risk control triggers

	// Position mode
	IsCrossMargin bool // true=cross margin mode, false=isolated margin mode

	// Competition visibility
	ShowInCompetition bool // Whether to show in competition page

	// Strategy configuration (use complete strategy config)
	StrategyConfig *store.StrategyConfig // Strategy configuration (includes coin sources, indicators, risk control, prompts, etc.)
}

// AutoTrader automatic trader
type AutoTrader struct {
	id                    string // Trader unique identifier
	name                  string // Trader display name
	aiModel               string // AI model name
	exchange              string // Trading platform type (binance/bybit/etc)
	exchangeID            string // Exchange account UUID
	showInCompetition     bool   // Whether to show in competition page
	config                AutoTraderConfig
	trader                Trader // Use Trader interface (supports multiple platforms)
	mcpClient             mcp.AIClient
	store                 *store.Store             // Data storage (decision records, etc.)
	strategyEngine        *kernel.StrategyEngine // Strategy engine (uses strategy configuration)
	cycleNumber           int                      // Current cycle number
	initialBalance        float64
	dailyPnL              float64
	customPrompt          string // Custom trading strategy prompt
	overrideBasePrompt    bool   // Whether to override base prompt
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	isRunningMutex        sync.RWMutex       // Mutex to protect isRunning flag
	startTime             time.Time          // System start time
	callCount             int                // AI call count
	positionFirstSeenTime map[string]int64   // Position first seen time (symbol_side -> timestamp in milliseconds)
	stopMonitorCh         chan struct{}      // Used to stop monitoring goroutine
	monitorWg             sync.WaitGroup     // Used to wait for monitoring goroutine to finish
	peakPnLCache          map[string]float64 // Peak profit (MFE) cache: symbol_side -> max P&L %
	peakPnLCacheMutex     sync.RWMutex
	bottomPnLCache        map[string]float64 // Bottom (MAE) cache: symbol_side -> min P&L %
	bottomPnLCacheMutex   sync.RWMutex
	lastBalanceSyncTime   time.Time          // Last balance sync time
	userID                string             // User ID
	gridState             *GridState         // Grid trading state (only used when StrategyType == "grid_trading")
	watchdogCtx           context.Context    // Context for risk watchdog (cancelled on Stop)
	watchdogCancel        context.CancelFunc // Cancel risk watchdog on Stop
	atrTrailingState      map[string]*ATRTrailingState // symbol_side -> state（ATR 移动止盈止损）
	atrTrailingMu         sync.RWMutex
	atrTrailingCtx        context.Context
	atrTrailingCancel     context.CancelFunc
	isExecuting           atomic.Bool       // 引擎互斥锁：防止多个 AI 决策线程重叠
	peakBottomCh          chan peakBottomUpdate        // 异步极值更新，不阻塞 runCycle
}

// peakBottomUpdate 供监控协程批量更新 MFE/MAE 缓存
type peakBottomUpdate struct {
	Symbol  string
	Side    string
	PnlPct  float64
}

// resolveInitialBalanceForConfig 确定 PnL 分母（初始本金）：模拟盘必须用 VirtualEquity，禁止用实盘余额
func resolveInitialBalanceForConfig(config AutoTraderConfig) float64 {
	if config.IsDryRun {
		if config.VirtualEquity > 0 {
			return config.VirtualEquity
		}
		return 10000
	}
	return config.InitialBalance
}

// NewAutoTrader creates an automatic trader
// st parameter is used to store decision records to database
func NewAutoTrader(config AutoTraderConfig, st *store.Store, userID string) (*AutoTrader, error) {
	// Set default values
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}

	// Initialize AI client based on provider
	var mcpClient mcp.AIClient
	aiModel := config.AIModel
	if config.UseQwen && aiModel == "" {
		aiModel = "qwen"
	}

	switch aiModel {
	case "claude":
		mcpClient = mcp.NewClaudeClient()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using Claude AI", config.Name)

	case "kimi":
		mcpClient = mcp.NewKimiClient()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using Kimi (Moonshot) AI", config.Name)

	case "gemini":
		mcpClient = mcp.NewGeminiClient()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using Google Gemini AI", config.Name)

	case "grok":
		mcpClient = mcp.NewGrokClient()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using xAI Grok AI", config.Name)

	case "openai":
		mcpClient = mcp.NewOpenAIClient()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using OpenAI", config.Name)

	case "minimax":
		// MiniMax Coding Plan 走 Anthropic Messages 协议，复用 Claude Client
		mcpClient = mcp.NewClaudeClient()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using MiniMax (Anthropic-compatible)", config.Name)

	case "qwen":
		mcpClient = mcp.NewQwenClient()
		apiKey := config.QwenKey
		if apiKey == "" {
			apiKey = config.CustomAPIKey
		}
		mcpClient.SetAPIKey(apiKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using Alibaba Cloud Qwen AI", config.Name)

	case "custom":
		mcpClient = mcp.New()
		mcpClient.SetAPIKey(config.CustomAPIKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using custom AI API: %s (model: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)

	default: // deepseek or empty
		mcpClient = mcp.NewDeepSeekClient()
		apiKey := config.DeepSeekKey
		if apiKey == "" {
			apiKey = config.CustomAPIKey
		}
		mcpClient.SetAPIKey(apiKey, config.CustomAPIURL, config.CustomModelName)
		logger.Infof("🤖 [%s] Using DeepSeek AI", config.Name)
	}

	if config.CustomAPIURL != "" || config.CustomModelName != "" {
		logger.Infof("🔧 [%s] Custom config - URL: %s, Model: %s", config.Name, config.CustomAPIURL, config.CustomModelName)
	}

	// Set default trading platform
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// Create corresponding trader based on configuration
	var trader Trader
	var err error

	// Record position mode (general)
	marginModeStr := "Cross Margin"
	if !config.IsCrossMargin {
		marginModeStr = "Isolated Margin"
	}
	logger.Infof("📊 [%s] Position mode: %s", config.Name, marginModeStr)

	switch config.Exchange {
	case "binance":
		logger.Infof("🏦 [%s] Using Binance Futures trading (testnet=%v)", config.Name, config.BinanceTestnet)
		trader = binance.NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey, userID, config.BinanceTestnet)
	case "bybit":
		logger.Infof("🏦 [%s] Using Bybit Futures trading", config.Name)
		trader = bybit.NewBybitTrader(config.BybitAPIKey, config.BybitSecretKey)
	case "okx":
		logger.Infof("🏦 [%s] Using OKX Futures trading (testnet=%v)", config.Name, config.OKXTestnet)
		trader = okx.NewOKXTrader(config.OKXAPIKey, config.OKXSecretKey, config.OKXPassphrase, config.IsCrossMargin, config.OKXTestnet)
	case "bitget":
		logger.Infof("🏦 [%s] Using Bitget Futures trading", config.Name)
		trader = bitget.NewBitgetTrader(config.BitgetAPIKey, config.BitgetSecretKey, config.BitgetPassphrase)
	case "gate":
		logger.Infof("🏦 [%s] Using Gate.io Futures trading", config.Name)
		trader = gate.NewGateTrader(config.GateAPIKey, config.GateSecretKey)
	case "kucoin":
		logger.Infof("🏦 [%s] Using KuCoin Futures trading", config.Name)
		trader = kucoin.NewKuCoinTrader(config.KuCoinAPIKey, config.KuCoinSecretKey, config.KuCoinPassphrase)
	case "hyperliquid":
		logger.Infof("🏦 [%s] Using Hyperliquid trading", config.Name)
		trader, err = hyperliquid.NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidWalletAddr, config.HyperliquidTestnet)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Hyperliquid trader: %w", err)
		}
	case "aster":
		logger.Infof("🏦 [%s] Using Aster trading", config.Name)
		trader, err = aster.NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Aster trader: %w", err)
		}
	case "lighter":
		logger.Infof("🏦 [%s] Using LIGHTER trading", config.Name)

		if config.LighterWalletAddr == "" || config.LighterAPIKeyPrivateKey == "" {
			return nil, fmt.Errorf("Lighter requires wallet address and API Key private key")
		}

		// Lighter only supports mainnet (testnet disabled)
		trader, err = lighter.NewLighterTraderV2(
			config.LighterWalletAddr,
			config.LighterAPIKeyPrivateKey,
			config.LighterAPIKeyIndex,
			false, // Always use mainnet for Lighter
		)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize LIGHTER trader: %w", err)
		}
		logger.Infof("✓ LIGHTER trader initialized successfully")
	default:
		return nil, fmt.Errorf("unsupported trading platform: %s", config.Exchange)
	}

	// Validate initial balance configuration (实盘：为 0 时从交易所拉取；模拟盘不拉取，使用 VirtualEquity)
	if !config.IsDryRun && config.InitialBalance <= 0 {
		logger.Infof("📊 [%s] Initial balance not set, attempting to fetch current balance from exchange...", config.Name)
		account, err := trader.GetBalance()
		if err != nil {
			return nil, fmt.Errorf("initial balance not set and unable to fetch balance from exchange: %w", err)
		}
		// Try multiple balance field names (different exchanges return different formats)
		balanceKeys := []string{"total_equity", "totalWalletBalance", "wallet_balance", "totalEq", "balance"}
		var foundBalance float64
		for _, key := range balanceKeys {
			if balance, ok := account[key].(float64); ok && balance > 0 {
				foundBalance = balance
				break
			}
		}
		if foundBalance > 0 {
			config.InitialBalance = foundBalance
			logger.Infof("✓ [%s] Auto-fetched initial balance: %.2f USDT", config.Name, foundBalance)
			// Save to database so it persists across restarts
			if st != nil {
				if err := st.Trader().UpdateInitialBalance(userID, config.ID, foundBalance); err != nil {
					logger.Infof("⚠️  [%s] Failed to save initial balance to database: %v", config.Name, err)
				} else {
					logger.Infof("✓ [%s] Initial balance saved to database", config.Name)
				}
			}
		} else {
			return nil, fmt.Errorf("initial balance must be greater than 0, please set InitialBalance in config or ensure exchange account has balance")
		}
	}

	// Get last cycle number (for recovery)
	var cycleNumber int
	if st != nil {
		cycleNumber, _ = st.Decision().GetLastCycleNumber(config.ID)
		logger.Infof("📊 [%s] Decision records will be stored to database", config.Name)
	}

	// Create strategy engine (must have strategy config)
	if config.StrategyConfig == nil {
		return nil, fmt.Errorf("[%s] strategy not configured", config.Name)
	}
	strategyEngine := kernel.NewStrategyEngine(config.StrategyConfig)
	logger.Infof("✓ [%s] Using strategy engine (strategy configuration loaded)", config.Name)

	return &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		exchangeID:            config.ExchangeID,
		showInCompetition:     config.ShowInCompetition,
		config:                config,
		trader:                trader,
		mcpClient:             mcpClient,
		store:                 st,
		strategyEngine:        strategyEngine,
		cycleNumber:           cycleNumber,
		initialBalance:        resolveInitialBalanceForConfig(config),
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
		atrTrailingState:      make(map[string]*ATRTrailingState),
		stopMonitorCh:         make(chan struct{}),
		monitorWg:             sync.WaitGroup{},
		peakPnLCache:          make(map[string]float64),
		peakPnLCacheMutex:     sync.RWMutex{},
		bottomPnLCache:        make(map[string]float64),
		bottomPnLCacheMutex:   sync.RWMutex{},
		peakBottomCh:          make(chan peakBottomUpdate, 256),
		lastBalanceSyncTime:   time.Now(),
		userID:                userID,
	}, nil
}

// Run runs the automatic trading main loop
func (at *AutoTrader) Run() error {
	at.isRunningMutex.Lock()
	at.isRunning = true
	at.isRunningMutex.Unlock()

	at.stopMonitorCh = make(chan struct{})
	at.startTime = time.Now()

	logger.Info("🚀 AI-driven automatic trading system started")
	logger.Infof("💰 Initial balance: %.2f USDT", at.initialBalance)
	logger.Infof("⚙️  Scan interval: %v", at.config.ScanInterval)
	logger.Info("🤖 AI will make full decisions on leverage, position size, stop loss/take profit, etc.")
	at.monitorWg.Add(1)
	defer at.monitorWg.Done()

	// 异步极值更新协程：不阻塞主交易循环 runCycle
	at.monitorWg.Add(1)
	go at.runPeakBottomWorker()

	// Start drawdown monitoring
	at.startDrawdownMonitor()

	// Start Lighter order sync if using Lighter exchange
	if at.exchange == "lighter" {
		if lighterTrader, ok := at.trader.(*lighter.LighterTraderV2); ok && at.store != nil {
			lighterTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] Lighter order+position sync enabled (every 30s)", at.name)
		}
	}

	// Start Hyperliquid order sync if using Hyperliquid exchange
	if at.exchange == "hyperliquid" {
		if hyperliquidTrader, ok := at.trader.(*hyperliquid.HyperliquidTrader); ok && at.store != nil {
			hyperliquidTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] Hyperliquid order+position sync enabled (every 30s)", at.name)
		}
	}

	// Start Bybit order sync if using Bybit exchange
	if at.exchange == "bybit" {
		if bybitTrader, ok := at.trader.(*bybit.BybitTrader); ok && at.store != nil {
			bybitTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] Bybit order+position sync enabled (every 30s)", at.name)
		}
	}

	// Start OKX order sync if using OKX exchange（模拟盘拉取 fills 易触发 50111 限频，间隔改为 2 分钟）
	if at.exchange == "okx" {
		if okxTrader, ok := at.trader.(*okx.OKXTrader); ok && at.store != nil {
			interval := 30 * time.Second
			if okxTrader.IsTestnet() {
				interval = 2 * time.Minute
			}
			okxTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, interval)
			logger.Infof("🔄 [%s] OKX order+position sync enabled (interval: %v)", at.name, interval)
		}
	}

	// Start Bitget order sync if using Bitget exchange
	if at.exchange == "bitget" {
		if bitgetTrader, ok := at.trader.(*bitget.BitgetTrader); ok && at.store != nil {
			bitgetTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] Bitget order+position sync enabled (every 30s)", at.name)
		}
	}

	// Start Aster order sync if using Aster exchange
	if at.exchange == "aster" {
		if asterTrader, ok := at.trader.(*aster.AsterTrader); ok && at.store != nil {
			asterTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] Aster order+position sync enabled (every 30s)", at.name)
		}
	}

	// Start Binance order sync and cache self-healing if using Binance exchange
	if at.exchange == "binance" {
		if binanceTrader, ok := at.trader.(*binance.FuturesTrader); ok {
			if at.store != nil {
				binanceTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
				logger.Infof("🔄 [%s] Binance order+position sync enabled (every 30s)", at.name)
			}
			// 定时全局对账：每 5 分钟 REST 全量覆盖缓存，应对 WS 漏接或手机端手动操作
			at.monitorWg.Add(1)
			go func() {
				defer at.monitorWg.Done()
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-at.stopMonitorCh:
						return
					case <-ticker.C:
						binanceTrader.ReconcileFromREST()
						logger.Infof("🔄 [%s] Cache self-healing: 5-min REST reconciliation done", at.name)
					}
				}
			}()
			logger.Infof("🔄 [%s] Cache self-healing enabled (every 5 min REST reconciliation)", at.name)
		}
	}

	// Start Gate order sync if using Gate exchange
	if at.exchange == "gate" {
		if gateTrader, ok := at.trader.(*gate.GateTrader); ok && at.store != nil {
			gateTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] Gate order+position sync enabled (every 30s)", at.name)
		}
	}

	// Start KuCoin order sync if using KuCoin exchange
	if at.exchange == "kucoin" {
		if kucoinTrader, ok := at.trader.(*kucoin.KuCoinTrader); ok && at.store != nil {
			kucoinTrader.StartOrderSync(at.id, at.exchangeID, at.exchange, at.store, 30*time.Second)
			logger.Infof("🔄 [%s] KuCoin order+position sync enabled (every 30s)", at.name)
		}
	}

	ticker := time.NewTicker(at.config.ScanInterval)
	defer ticker.Stop()

	// Check if this is a grid trading strategy
	isGridStrategy := at.IsGridStrategy()
	// 脱机风控狗：指标移动止盈止损（不经过 AI，仅 AI 策略且开启时启动）
	if !isGridStrategy && at.config.StrategyConfig != nil && at.config.StrategyConfig.Indicators.EnableIndicatorTrailing {
		at.watchdogCtx, at.watchdogCancel = context.WithCancel(context.Background())
		indicatorName := at.config.StrategyConfig.Indicators.TrailingIndicator
		if indicatorName == "" {
			indicatorName = "ema_20"
		}
		RunRiskWatchdog(at.watchdogCtx, at.trader, func() *store.StrategyConfig { return at.config.StrategyConfig }, func() string { return at.GetExchange() }, func(symbol, action string, order map[string]interface{}, quantity, exitPrice, entryPrice float64) {
			at.recordAndConfirmOrder(order, symbol, action, quantity, exitPrice, 0, entryPrice)
		})
		logger.Infof("🛡️ [%s] Risk Watchdog (Trailing Indicator) started, indicator=%s", at.name, indicatorName)
	}
	if !isGridStrategy && at.config.StrategyConfig != nil && at.config.StrategyConfig.Indicators.EnableATRTrailing {
		at.atrTrailingCtx, at.atrTrailingCancel = context.WithCancel(context.Background())
		RunATRTrailingWatchdog(at.atrTrailingCtx, at.trader,
			func() *store.StrategyConfig { return at.config.StrategyConfig },
			func() string { return at.GetExchange() },
			at.getATRTrailingStateSnapshot,
			at.onATRPartialClose,
			at.onATRFullClose,
		)
		logger.Infof("🛡️ [%s] ATR Trailing Watchdog started", at.name)
	}
	if isGridStrategy {
		logger.Infof("🔲 [%s] Grid trading strategy detected, initializing grid...", at.name)
		if err := at.InitializeGrid(); err != nil {
			logger.Errorf("❌ [%s] Failed to initialize grid: %v", at.name, err)
			return fmt.Errorf("grid initialization failed: %w", err)
		}
	}

	// Execute immediately on first run
	if isGridStrategy {
		if err := at.RunGridCycle(); err != nil {
			logger.Infof("❌ Grid execution failed: %v", err)
		}
	} else {
		if err := at.runCycle(); err != nil {
			logger.Infof("❌ Execution failed: %v", err)
		}
	}

	for {
		at.isRunningMutex.RLock()
		running := at.isRunning
		at.isRunningMutex.RUnlock()

		if !running {
			break
		}

		select {
		case <-ticker.C:
			if isGridStrategy {
				if err := at.RunGridCycle(); err != nil {
					logger.Infof("❌ Grid execution failed: %v", err)
				}
			} else {
				if err := at.runCycle(); err != nil {
					logger.Infof("❌ Execution failed: %v", err)
				}
			}
		case <-at.stopMonitorCh:
			logger.Infof("[%s] ⏹ Stop signal received, exiting automatic trading main loop", at.name)
			return nil
		}
	}

	return nil
}

// Stop stops the automatic trading
func (at *AutoTrader) Stop() {
	at.isRunningMutex.Lock()
	if !at.isRunning {
		at.isRunningMutex.Unlock()
		return
	}
	at.isRunning = false
	at.isRunningMutex.Unlock()

	if at.watchdogCancel != nil {
		at.watchdogCancel()
		at.watchdogCancel = nil
	}
	if at.atrTrailingCancel != nil {
		at.atrTrailingCancel()
		at.atrTrailingCancel = nil
	}
	close(at.stopMonitorCh) // Notify monitoring goroutine to stop
	at.monitorWg.Wait()     // Wait for monitoring goroutine to finish
	logger.Info("⏹ Automatic trading system stopped")
}

// runCycle runs one trading cycle (using AI full decision-making)
func (at *AutoTrader) runCycle() error {
	// 引擎互斥锁：若上一周期仍在执行，跳过本次
	if at.isExecuting.Load() {
		logger.Infof("⏭ Skip cycle: previous AI decision still executing")
		return nil
	}
	at.isExecuting.Store(true)
	defer at.isExecuting.Store(false)

	at.callCount++

	logger.Info("\n" + strings.Repeat("=", 70) + "\n")
	logger.Infof("⏰ %s - AI decision cycle #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	logger.Info(strings.Repeat("=", 70))

	// 0. Check if trader is stopped (early exit to prevent trades after Stop() is called)
	at.isRunningMutex.RLock()
	running := at.isRunning
	at.isRunningMutex.RUnlock()
	if !running {
		logger.Infof("⏹ Trader is stopped, aborting cycle #%d", at.callCount)
		return nil
	}

	// Create decision record
	record := &store.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. Check if trading needs to be stopped
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		logger.Infof("⏸ Risk control: Trading paused, remaining %.0f minutes", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Risk control paused, remaining %.0f minutes", remaining.Minutes())
		at.saveDecision(record)
		return nil
	}

	// 2. Reset daily P&L (reset every day)
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		logger.Info("📅 Daily P&L reset")
	}

	// 4. Collect trading context（模拟盘必须走虚拟本金与 dry_run 数据，禁止混用实盘余额）
	var ctx *kernel.Context
	var err error
	if at.config.IsDryRun {
		ctx, err = at.buildDryRunTradingContext()
	} else {
		ctx, err = at.buildTradingContext()
	}
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Failed to build trading context: %v", err)
		at.saveDecision(record)
		return fmt.Errorf("failed to build trading context: %w", err)
	}

	// Save equity snapshot independently (decoupled from AI decision, used for drawing profit curve)
	// NOTE: Must be called BEFORE candidate coins check to ensure equity is always recorded
	at.saveEquitySnapshot(ctx)

	// 如果没有候选币种，记录但不报错
	if len(ctx.CandidateCoins) == 0 {
		logger.Infof("ℹ️  No candidate coins available, skipping this cycle")
		record.Success = true // 不是错误，只是没有候选币
		record.ExecutionLog = append(record.ExecutionLog, "No candidate coins available, cycle skipped")
		record.AccountState = store.AccountSnapshot{
			TotalBalance:          ctx.Account.TotalEquity,
			AvailableBalance:      ctx.Account.AvailableBalance,
			TotalUnrealizedProfit: ctx.Account.UnrealizedPnL,
			PositionCount:         ctx.Account.PositionCount,
			InitialBalance:        at.initialBalance,
		}
		at.saveDecision(record)
		return nil
	}

	logger.Info(strings.Repeat("=", 70))
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}

	logger.Infof("📊 Account equity: %.2f USDT | Available: %.2f USDT | Positions: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 5. Use strategy engine to call AI for decision
	logger.Infof("🤖 Requesting AI analysis and decision... [Strategy Engine]")
	aiDecision, err := kernel.GetFullDecisionWithStrategy(ctx, at.mcpClient, at.strategyEngine, "balanced")

	if aiDecision != nil && aiDecision.AIRequestDurationMs > 0 {
		record.AIRequestDurationMs = aiDecision.AIRequestDurationMs
		logger.Infof("⏱️ AI call duration: %.2f seconds", float64(record.AIRequestDurationMs)/1000)
		record.ExecutionLog = append(record.ExecutionLog,
			fmt.Sprintf("AI call duration: %d ms", record.AIRequestDurationMs))
	}

	// Save chain of thought, decisions, and input prompt even if there's an error (for debugging)
	if aiDecision != nil {
		record.SystemPrompt = aiDecision.SystemPrompt // Save system prompt
		record.InputPrompt = aiDecision.UserPrompt
		record.CoTTrace = aiDecision.CoTTrace
		record.RawResponse = aiDecision.RawResponse // Save raw AI response for debugging
		if len(aiDecision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(aiDecision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("Failed to get AI decision: %v", err)

		// Print system prompt and AI chain of thought (output even with errors for debugging)
		if aiDecision != nil {
			logger.Info("\n" + strings.Repeat("=", 70) + "\n")
			logger.Infof("📋 System prompt (error case)")
			logger.Info(strings.Repeat("=", 70))
			logger.Info(aiDecision.SystemPrompt)
			logger.Info(strings.Repeat("=", 70))

			if aiDecision.CoTTrace != "" {
				logger.Info("\n" + strings.Repeat("-", 70) + "\n")
				logger.Info("💭 AI chain of thought analysis (error case):")
				logger.Info(strings.Repeat("-", 70))
				logger.Info(aiDecision.CoTTrace)
				logger.Info(strings.Repeat("-", 70))
			}
		}

		at.saveDecision(record)
		return fmt.Errorf("failed to get AI decision: %w", err)
	}

	// // 5. Print system prompt
	// logger.Infof("\n" + strings.Repeat("=", 70))
	// logger.Infof("📋 System prompt [template: %s]", at.systemPromptTemplate)
	// logger.Info(strings.Repeat("=", 70))
	// logger.Info(decision.SystemPrompt)
	// logger.Infof(strings.Repeat("=", 70) + "\n")

	// 6. Print AI chain of thought
	// logger.Infof("\n" + strings.Repeat("-", 70))
	// logger.Info("💭 AI chain of thought analysis:")
	// logger.Info(strings.Repeat("-", 70))
	// logger.Info(decision.CoTTrace)
	// logger.Infof(strings.Repeat("-", 70) + "\n")

	// 7. Print AI decisions
	// logger.Infof("📋 AI decision list (%d items):\n", len(kernel.Decisions))
	// for i, d := range kernel.Decisions {
	//     logger.Infof("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
	//     if d.Action == "open_long" || d.Action == "open_short" {
	//        logger.Infof("      Leverage: %dx | Position: %.2f USDT | Stop loss: %.4f | Take profit: %.4f",
	//           d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
	//     }
	// }
	logger.Info()
	logger.Info(strings.Repeat("-", 70))
	// 8. Sort decisions: ensure close positions first, then open positions (prevent position stacking overflow)
	logger.Info(strings.Repeat("-", 70))

	// 8. Sort decisions: ensure close positions first, then open positions (prevent position stacking overflow)
	sortedDecisions := sortDecisionsByPriority(aiDecision.Decisions)

	logger.Info("🔄 Execution order (optimized): Close positions first → Open positions later")
	for i, d := range sortedDecisions {
		logger.Infof("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	logger.Info()

	// Check if trader is stopped before executing any decisions (prevent trades after Stop())
	at.isRunningMutex.RLock()
	running = at.isRunning
	at.isRunningMutex.RUnlock()
	if !running {
		logger.Infof("⏹ Trader stopped before decision execution, aborting cycle #%d", at.callCount)
		return nil
	}

	// Execute decisions and record results
	for _, d := range sortedDecisions {
		// Check if trader is stopped before each decision (allow immediate stop during execution)
		at.isRunningMutex.RLock()
		running = at.isRunning
		at.isRunningMutex.RUnlock()
		if !running {
			logger.Infof("⏹ Trader stopped during decision execution, aborting remaining decisions")
			break
		}

		actionRecord := store.DecisionAction{
			Action:     d.Action,
			Symbol:     d.Symbol,
			Quantity:   0,
			Leverage:   d.Leverage,
			Price:      0,
			StopLoss:   d.StopLoss,
			TakeProfit: d.TakeProfit,
			Confidence: d.Confidence,
			Reasoning:  d.Reasoning,
			Timestamp:  time.Now().UTC(),
			Success:    false,
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord, aiDecision.CoTTrace); err != nil {
			logger.Infof("❌ Failed to execute decision (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s failed: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("✓ %s %s succeeded", d.Symbol, d.Action))
			// Brief delay after successful execution
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 9. Save decision record
	if err := at.saveDecision(record); err != nil {
		logger.Infof("⚠ Failed to save decision record: %v", err)
	}

	return nil
}

// buildDryRunTradingContext 模拟盘专用：资金用 VirtualEquity，持仓从 DB 取并用当前行情算浮盈（绝不调用交易所 GetBalance）
func (at *AutoTrader) buildDryRunTradingContext() (*kernel.Context, error) {
	totalEquity := at.config.VirtualEquity
	if totalEquity <= 0 {
		totalEquity = 10000
		if at.store != nil {
			if full, _ := at.store.Trader().GetFullConfig(at.userID, at.id); full != nil && full.Trader != nil && full.Trader.VirtualEquity > 0 {
				totalEquity = full.Trader.VirtualEquity
			} else {
				_ = at.store.Trader().UpdateVirtualEquity(at.userID, at.id, totalEquity)
			}
		}
		at.config.VirtualEquity = totalEquity
	}
	logger.Infof("[DryRun] Using virtual equity: %.2f", totalEquity)

	openPositions, err := at.store.Position().GetOpenPositions(at.id)
	if err != nil {
		return nil, fmt.Errorf("dry run get open positions: %w", err)
	}

	var positionInfos []kernel.PositionInfo
	totalMarginUsed := 0.0
	totalUnrealizedProfit := 0.0

	for _, pos := range openPositions {
		if pos.Source != "dry_run" {
			continue
		}
		data, err := market.GetWithExchange(pos.Symbol, at.exchange, nil)
		if err != nil {
			continue
		}
		markPrice := data.CurrentPrice
		leverage := pos.Leverage
		if leverage <= 0 {
			leverage = 10
		}
		marginUsed := (pos.Quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		var unrealizedPnl float64
		if pos.Side == "LONG" {
			unrealizedPnl = (markPrice - pos.EntryPrice) * pos.Quantity
		} else {
			unrealizedPnl = (pos.EntryPrice - markPrice) * pos.Quantity
		}
		totalUnrealizedProfit += unrealizedPnl
		pnlPct := 0.0
		if marginUsed > 0 {
			pnlPct = (unrealizedPnl / marginUsed) * 100
		}
		// DryRun 每轮异步更新内存极值，不阻塞 runCycle
		at.submitPeakBottomUpdate(pos.Symbol, pos.Side, pnlPct)

		posKey := pos.Symbol + "_" + strings.ToLower(pos.Side)
		at.peakPnLCacheMutex.RLock()
		peakPct := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()

		positionInfos = append(positionInfos, kernel.PositionInfo{
			Symbol:           pos.Symbol,
			Side:             strings.ToLower(pos.Side),
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        markPrice,
			Quantity:         pos.Quantity,
			Leverage:         leverage,
			UnrealizedPnL:    unrealizedPnl,
			UnrealizedPnLPct: pnlPct,
			PeakPnLPct:       peakPct,
			MarginUsed:       marginUsed,
			UpdateTime:       pos.UpdatedAt,
		})
	}

	availableBalance := totalEquity - totalMarginUsed
	if availableBalance < 0 {
		availableBalance = 0
	}
	// PnL 分母必须为初始虚拟本金（TotalPnL / InitialVirtualEquity），禁止用实盘余额
	initialVirtualEquity := at.initialBalance
	if initialVirtualEquity <= 0 {
		initialVirtualEquity = totalEquity
		at.initialBalance = totalEquity
	}
	totalPnL := totalEquity - initialVirtualEquity
	totalPnLPct := 0.0
	if initialVirtualEquity > 0 {
		totalPnLPct = (totalPnL / initialVirtualEquity) * 100
	}
	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	btcEthLeverage, altcoinLeverage := 5, 5
	if at.strategyEngine != nil {
		cfg := at.strategyEngine.GetConfig()
		btcEthLeverage = cfg.RiskControl.BTCETHMaxLeverage
		altcoinLeverage = cfg.RiskControl.AltcoinMaxLeverage
	}

	var candidateCoins []kernel.CandidateCoin
	if at.strategyEngine != nil {
		coins, _ := at.strategyEngine.GetCandidateCoins()
		candidateCoins = coins
	}

	ctx := &kernel.Context{
		CurrentTime:     time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  btcEthLeverage,
		AltcoinLeverage: altcoinLeverage,
		Account: kernel.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			UnrealizedPnL:    totalUnrealizedProfit,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
	}

	if at.store != nil {
		tradesWithReasoning, _ := at.store.Position().GetRecentTradesWithReasoningBySource(at.id, 10, "dry_run")
		for _, tr := range tradesWithReasoning {
			trade := tr.RecentTrade
			entryTimeStr := ""
			if trade.EntryTime > 0 {
				entryTimeStr = time.Unix(trade.EntryTime, 0).UTC().Format("01-02 15:04 UTC")
			}
			exitTimeStr := ""
			if trade.ExitTime > 0 {
				exitTimeStr = time.Unix(trade.ExitTime, 0).UTC().Format("01-02 15:04 UTC")
			}
			ctx.RecentOrders = append(ctx.RecentOrders, kernel.RecentOrder{
				Symbol: trade.Symbol, Side: trade.Side, EntryPrice: trade.EntryPrice, ExitPrice: trade.ExitPrice,
				RealizedPnL: trade.RealizedPnL, PnLPct: trade.PnLPct, EntryTime: entryTimeStr, ExitTime: exitTimeStr, HoldDuration: trade.HoldDuration,
			})
			// 供「Recent AI Reasoning History」：开仓时的逻辑与盈亏结果（原证，防串线）
			resultStr := "PROFIT"
			if trade.PnLPct < 0 {
				resultStr = "LOSS"
			}
			ctx.RecentReasoningHistory = append(ctx.RecentReasoningHistory, kernel.ReasoningOutcome{
				PositionID:   tr.PositionID,
				EntryTimeStr: entryTimeStr,
				Side:         strings.ToUpper(trade.Side),
				Symbol:       trade.Symbol,
				Reasoning:    tr.AiReasoningAtOpen,
				ResultStr:    fmt.Sprintf("%s (%+.2f%%)", resultStr, trade.PnLPct),
				CloseReason:  tr.CloseReason,
			})
		}
		// 未决策期间空档复盘：自上次 AI 决策以来被系统平仓的数量（仅 dry_run）
		if lastMs, ok := at.store.Decision().GetLatestDecisionTimeMs(at.id); ok {
			ctx.ClosedCountSinceLastDecision, _ = at.store.Position().GetClosedCountSinceBySource(at.id, lastMs, "dry_run")
		}
		// 当前持仓的开仓逻辑（仅 dry_run 仓位，防与实盘混线）
		for _, op := range openPositions {
			if op.Source != "dry_run" {
				continue
			}
			ctx.OpenPositionReasoning = append(ctx.OpenPositionReasoning, kernel.OpenPositionReasoning{
				Symbol:    op.Symbol,
				Side:      op.Side,
				Reasoning: op.AiReasoningAtOpen,
			})
		}
		// 统计信息仅来自 source='dry_run' 的已平仓订单
		stats, _ := at.store.Position().GetFullStatsBySource(at.id, "dry_run")
		if stats != nil {
			ctx.TradingStats = &kernel.TradingStats{
				TotalTrades: stats.TotalTrades, WinRate: stats.WinRate, ProfitFactor: stats.ProfitFactor,
				SharpeRatio: stats.SharpeRatio, TotalPnL: stats.TotalPnL, AvgWin: stats.AvgWin, AvgLoss: stats.AvgLoss, MaxDrawdownPct: stats.MaxDrawdownPct,
			}
			// 无已平仓单时 PnL% 强制为 0，禁止用余额差值推算
			if stats.TotalTrades == 0 {
				ctx.Account.TotalPnLPct = 0
			}
		}
	}
	return ctx, nil
}

// buildTradingContext builds trading context（仅实盘：从交易所拉取余额与持仓，模拟盘勿调用）
func (at *AutoTrader) buildTradingContext() (*kernel.Context, error) {
	// 1. Get account information (exchange API)
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get account balance: %w", err)
	}

	// Get account fields
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0
	totalEquity := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Use totalEquity directly if provided by trader (more accurate)
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		totalEquity = eq
	} else {
		// Fallback: Total Equity = Wallet balance + Unrealized profit
		totalEquity = totalWalletBalance + totalUnrealizedProfit
	}

	// 2. Get position information
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	var positionInfos []kernel.PositionInfo
	totalMarginUsed := 0.0

	// Current position key set (for cleaning up closed position records)
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // Short position quantity is negative, convert to positive
		}

		// Skip closed positions (quantity = 0), prevent "ghost positions" from being passed to AI
		if quantity == 0 {
			continue
		}

		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// Calculate margin used (estimated)
		leverage := 10 // Default value, should actually be fetched from position info
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		// Calculate P&L percentage (based on margin, considering leverage)
		pnlPct := calculatePnLPercentage(unrealizedPnl, marginUsed)

		// Get position open time from exchange (preferred) or fallback to local tracking
		posKey := symbol + "_" + strings.ToLower(side)
		currentPositionKeys[posKey] = true

		var updateTime int64
		// Priority 1: Get from database (trader_positions table) - most accurate
		if at.store != nil {
			if dbPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, side); err == nil && dbPos != nil {
				if dbPos.EntryTime > 0 {
					updateTime = dbPos.EntryTime
				}
			}
		}
		// Priority 2: Get from exchange API (Bybit: createdTime, OKX: createdTime)
		if updateTime == 0 {
			if createdTime, ok := pos["createdTime"].(int64); ok && createdTime > 0 {
				updateTime = createdTime
			}
		}
		// Priority 3: Fallback to local tracking
		if updateTime == 0 {
			if _, exists := at.positionFirstSeenTime[posKey]; !exists {
				at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
			}
			updateTime = at.positionFirstSeenTime[posKey]
		}

		// Get peak profit rate for this position
		at.peakPnLCacheMutex.RLock()
		peakPnlPct := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()

		positionInfos = append(positionInfos, kernel.PositionInfo{
			Symbol:           symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         quantity,
			Leverage:         leverage,
			UnrealizedPnL:    unrealizedPnl,
			UnrealizedPnLPct: pnlPct,
			PeakPnLPct:       peakPnlPct,
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       updateTime,
		})
	}

	// Clean up closed position records
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}

	// 3. Use strategy engine to get candidate coins (must have strategy engine)
	var candidateCoins []kernel.CandidateCoin
	if at.strategyEngine == nil {
		logger.Infof("⚠️ [%s] No strategy engine configured, skipping candidate coins", at.name)
	} else {
		coins, err := at.strategyEngine.GetCandidateCoins()
		if err != nil {
			// Log warning but don't fail - equity snapshot should still be saved
			logger.Infof("⚠️ [%s] Failed to get candidate coins: %v (will use empty list)", at.name, err)
		} else {
			candidateCoins = coins
			logger.Infof("📋 [%s] Strategy engine fetched candidate coins: %d", at.name, len(candidateCoins))
		}
	}

	// 4. Calculate total P&L
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. Get leverage from strategy config
	strategyConfig := at.strategyEngine.GetConfig()
	btcEthLeverage := strategyConfig.RiskControl.BTCETHMaxLeverage
	altcoinLeverage := strategyConfig.RiskControl.AltcoinMaxLeverage
	logger.Infof("📋 [%s] Strategy leverage config: BTC/ETH=%dx, Altcoin=%dx", at.name, btcEthLeverage, altcoinLeverage)

	// 6. Build context
	ctx := &kernel.Context{
		CurrentTime:     time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		RuntimeMinutes:  int(time.Since(at.startTime).Minutes()),
		CallCount:       at.callCount,
		BTCETHLeverage:  btcEthLeverage,
		AltcoinLeverage: altcoinLeverage,
		Account: kernel.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			UnrealizedPnL:    totalUnrealizedProfit,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:      positionInfos,
		CandidateCoins: candidateCoins,
	}

	// 7. Add recent closed trades (if store is available)
	if at.store != nil {
		// Get recent 10 closed trades with AI reasoning for AI context
		tradesWithReasoning, err := at.store.Position().GetRecentTradesWithReasoning(at.id, 10)
		if err != nil {
			logger.Infof("⚠️ [%s] Failed to get recent trades: %v", at.name, err)
		} else {
			logger.Infof("📊 [%s] Found %d recent closed trades for AI context", at.name, len(tradesWithReasoning))
			for _, tr := range tradesWithReasoning {
				trade := tr.RecentTrade
				entryTimeStr := ""
				if trade.EntryTime > 0 {
					entryTimeStr = time.Unix(trade.EntryTime, 0).UTC().Format("01-02 15:04 UTC")
				}
				exitTimeStr := ""
				if trade.ExitTime > 0 {
					exitTimeStr = time.Unix(trade.ExitTime, 0).UTC().Format("01-02 15:04 UTC")
				}
				ctx.RecentOrders = append(ctx.RecentOrders, kernel.RecentOrder{
					Symbol:       trade.Symbol,
					Side:         trade.Side,
					EntryPrice:   trade.EntryPrice,
					ExitPrice:    trade.ExitPrice,
					RealizedPnL:  trade.RealizedPnL,
					PnLPct:       trade.PnLPct,
					EntryTime:    entryTimeStr,
					ExitTime:     exitTimeStr,
					HoldDuration: trade.HoldDuration,
				})
				resultStr := "PROFIT"
				if trade.PnLPct < 0 {
					resultStr = "LOSS"
				}
				ctx.RecentReasoningHistory = append(ctx.RecentReasoningHistory, kernel.ReasoningOutcome{
					PositionID:   tr.PositionID,
					EntryTimeStr: entryTimeStr,
					Side:         strings.ToUpper(trade.Side),
					Symbol:       trade.Symbol,
					Reasoning:    tr.AiReasoningAtOpen,
					ResultStr:    fmt.Sprintf("%s (%+.2f%%)", resultStr, trade.PnLPct),
					CloseReason:  tr.CloseReason,
				})
			}
		}
		// 未决策期间空档复盘：自上次 AI 决策以来被系统平仓的数量
		if lastMs, ok := at.store.Decision().GetLatestDecisionTimeMs(at.id); ok {
			ctx.ClosedCountSinceLastDecision, _ = at.store.Position().GetClosedCountSince(at.id, lastMs)
		}
		// 当前持仓的开仓逻辑（你正在为什么而坚持）
		openPositions, errOpen := at.store.Position().GetOpenPositions(at.id)
		if errOpen == nil {
			for _, op := range openPositions {
				ctx.OpenPositionReasoning = append(ctx.OpenPositionReasoning, kernel.OpenPositionReasoning{
					Symbol:    op.Symbol,
					Side:      op.Side,
					Reasoning: op.AiReasoningAtOpen,
				})
			}
		}
		// Get trading statistics for AI context
		stats, err := at.store.Position().GetFullStats(at.id)
		if err != nil {
			logger.Infof("⚠️ [%s] Failed to get trading stats: %v", at.name, err)
		} else if stats == nil {
			logger.Infof("⚠️ [%s] GetFullStats returned nil", at.name)
		} else if stats.TotalTrades == 0 {
			logger.Infof("⚠️ [%s] GetFullStats returned 0 trades (traderID=%s)", at.name, at.id)
		} else {
			ctx.TradingStats = &kernel.TradingStats{
				TotalTrades:    stats.TotalTrades,
				WinRate:        stats.WinRate,
				ProfitFactor:   stats.ProfitFactor,
				SharpeRatio:    stats.SharpeRatio,
				TotalPnL:       stats.TotalPnL,
				AvgWin:         stats.AvgWin,
				AvgLoss:        stats.AvgLoss,
				MaxDrawdownPct: stats.MaxDrawdownPct,
			}
			logger.Infof("📈 [%s] Trading stats: %d trades, %.1f%% win rate, PF=%.2f, Sharpe=%.2f, DD=%.1f%%",
				at.name, stats.TotalTrades, stats.WinRate, stats.ProfitFactor, stats.SharpeRatio, stats.MaxDrawdownPct)
		}
	} else {
		logger.Infof("⚠️ [%s] Store is nil, cannot get recent trades", at.name)
	}

	// 8. Get quantitative data (if enabled in strategy config)
	if strategyConfig.Indicators.EnableQuantData {
		// Collect symbols to query (candidate coins + position coins)
		symbolsToQuery := make(map[string]bool)
		for _, coin := range candidateCoins {
			symbolsToQuery[coin.Symbol] = true
		}
		for _, pos := range positionInfos {
			symbolsToQuery[pos.Symbol] = true
		}

		symbols := make([]string, 0, len(symbolsToQuery))
		for sym := range symbolsToQuery {
			symbols = append(symbols, sym)
		}

		logger.Infof("📊 [%s] Fetching quantitative data for %d symbols...", at.name, len(symbols))
		ctx.QuantDataMap = at.strategyEngine.FetchQuantDataBatch(symbols)
		logger.Infof("📊 [%s] Successfully fetched quantitative data for %d symbols", at.name, len(ctx.QuantDataMap))
	}

	// 9. Get OI ranking data (market-wide position changes)
	if strategyConfig.Indicators.EnableOIRanking {
		logger.Infof("📊 [%s] Fetching OI ranking data...", at.name)
		ctx.OIRankingData = at.strategyEngine.FetchOIRankingData()
		if ctx.OIRankingData != nil {
			logger.Infof("📊 [%s] OI ranking data ready: %d top, %d low positions",
				at.name, len(ctx.OIRankingData.TopPositions), len(ctx.OIRankingData.LowPositions))
		}
	}

	// 10. Get NetFlow ranking data (market-wide fund flow)
	if strategyConfig.Indicators.EnableNetFlowRanking {
		logger.Infof("💰 [%s] Fetching NetFlow ranking data...", at.name)
		ctx.NetFlowRankingData = at.strategyEngine.FetchNetFlowRankingData()
		if ctx.NetFlowRankingData != nil {
			logger.Infof("💰 [%s] NetFlow ranking data ready: inst_in=%d, inst_out=%d",
				at.name, len(ctx.NetFlowRankingData.InstitutionFutureTop), len(ctx.NetFlowRankingData.InstitutionFutureLow))
		}
	}

	// 11. Get Price ranking data (market-wide gainers/losers)
	if strategyConfig.Indicators.EnablePriceRanking {
		logger.Infof("📈 [%s] Fetching Price ranking data...", at.name)
		ctx.PriceRankingData = at.strategyEngine.FetchPriceRankingData()
		if ctx.PriceRankingData != nil {
			logger.Infof("📈 [%s] Price ranking data ready for %d durations",
				at.name, len(ctx.PriceRankingData.Durations))
		}
	}

	return ctx, nil
}

// executeDecisionWithRecord executes AI decision and records detailed information.
// aiReasoning 为本轮 AI 思维链（CoTTrace），开仓时会写入仓位记录供复盘与自我修正。
func (at *AutoTrader) executeDecisionWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction, aiReasoning string) error {
	// 模拟盘：不调用交易所，走 Dry-Run 引擎（价格撮合 + 落库 + 虚拟资金）
	if at.config.IsDryRun {
		return at.executeDryRunOrder(decision, actionRecord, decision.Action, aiReasoning)
	}

	// Global guardrail: whether AI is allowed to issue manual close actions.
	enableAIClose := at.config.StrategyConfig != nil && at.config.StrategyConfig.RiskControl.EnableAIClose

	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord, aiReasoning)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord, aiReasoning)
	case "close_long":
		if !enableAIClose {
			logger.Infof("  ⛔ [RISK CONTROL] AI close_long blocked by config (enable_ai_close=false); ignoring action")
			return nil
		}
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		if !enableAIClose {
			logger.Infof("  ⛔ [RISK CONTROL] AI close_short blocked by config (enable_ai_close=false); ignoring action")
			return nil
		}
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "hold", "wait":
		// 动态止盈止损：若已有持仓且 AI 给出新 TP/SL / take_profit_stages 或 ATR 倍数，先撤旧单再挂新单（或仅更新 ATR 状态）
		hasTpSl := decision.TakeProfit > 0 || len(decision.TakeProfitStages) > 0 || decision.StopLoss > 0
		hasATR := at.config.StrategyConfig != nil && at.config.StrategyConfig.Indicators.EnableATRTrailing &&
			(decision.ATRTrailingSlMult > 0 || decision.ATRTrailingTpMult > 0 || len(decision.ATRTrailingTpStages) > 0)
		if (hasTpSl || hasATR) && at.updateTpSlForExistingPosition(decision) {
			// 已更新 TP/SL 或 ATR 状态，仅记录
		}
		return nil
	default:
		return fmt.Errorf("unknown action: %s", decision.Action)
	}
}

// getATRTrailingStateSnapshot 返回当前 ATR  trailing 状态的副本，供 watchdog 只读使用（避免并发写冲突）
func (at *AutoTrader) getATRTrailingStateSnapshot() map[string]*ATRTrailingState {
	at.atrTrailingMu.RLock()
	defer at.atrTrailingMu.RUnlock()
	out := make(map[string]*ATRTrailingState, len(at.atrTrailingState))
	for k, v := range at.atrTrailingState {
		if v != nil && v.CurrentQty > 0 {
			// 返回同一指针，watchdog 会通过 onPartialClose 回调让我们更新 CurrentQty/TriggeredStage
			out[k] = v
		}
	}
	return out
}

// getPositionQuantity 从交易所获取当前仓位数量（正数）。用于平仓前 clamping，避免“实际可平 < 计划平”导致 -2022。
func (at *AutoTrader) getPositionQuantity(symbol string, side string) (float64, bool) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return 0, false
	}
	sideLower := strings.ToLower(side)
	for _, pos := range positions {
		if pos["symbol"] != symbol {
			continue
		}
		s, _ := pos["side"].(string)
		if strings.ToLower(s) != sideLower {
			continue
		}
		amt, _ := pos["positionAmt"].(float64)
		if amt < 0 {
			amt = -amt
		}
		if amt > 0 {
			return amt, true
		}
		return 0, true
	}
	return 0, false
}

// getEntryATR 获取开仓时 ATR（用于 1R 首批止盈与追踪止损）。策略未配置 ATR 或获取失败时返回 0。
func (at *AutoTrader) getEntryATR(symbol string) float64 {
	if at.config.StrategyConfig == nil {
		return 0
	}
	opts := kernel.IndicatorParamsFromConfig(at.config.StrategyConfig.Indicators)
	data, err := market.GetWithExchange(symbol, at.exchange, opts)
	if err != nil || data == nil || data.DynamicIndicators == nil {
		return 0
	}
	if v, ok := data.DynamicIndicators["atr_14"]; ok && v > 0 {
		return v
	}
	return 0
}

func (at *AutoTrader) onATRPartialClose(symbol, side string, closedQty float64, stageIndex int) {
	key := symbol + "_" + strings.ToLower(side)
	at.atrTrailingMu.Lock()
	defer at.atrTrailingMu.Unlock()
	if s, ok := at.atrTrailingState[key]; ok && s != nil {
		s.CurrentQty -= closedQty
		if stageIndex >= 0 && stageIndex < 3 {
			s.TriggeredStage[stageIndex] = true
		}
		if s.CurrentQty <= 0 {
			delete(at.atrTrailingState, key)
		}
	}
}

func (at *AutoTrader) onATRFullClose(symbol, side string) {
	key := symbol + "_" + strings.ToLower(side)
	at.atrTrailingMu.Lock()
	defer at.atrTrailingMu.Unlock()
	delete(at.atrTrailingState, key)
}

// updateTpSlForExistingPosition 当已有持仓且 AI 给出新止盈/止损时：先撤旧 TP/SL 再挂新单。返回 true 表示已处理（含无持仓或失败仅打日志）
func (at *AutoTrader) updateTpSlForExistingPosition(decision *kernel.Decision) bool {
	cfg := at.config.StrategyConfig
	useATRTrailing := cfg != nil && cfg.Indicators.EnableATRTrailing &&
		(decision.ATRTrailingSlMult > 0 || decision.ATRTrailingTpMult > 0 || len(decision.ATRTrailingTpStages) > 0)

	positions, err := at.trader.GetPositions()
	if err != nil {
		logger.Infof("  ⚠️ [updateTpSl] get positions failed: %v", err)
		return false
	}
	var quantity float64
	var entryPrice float64
	var posSide string // "long" or "short"
	for _, pos := range positions {
		if pos["symbol"] != decision.Symbol {
			continue
		}
		side, _ := pos["side"].(string)
		if side != "long" && side != "short" {
			continue
		}
		amt, _ := pos["positionAmt"].(float64)
		if amt > 0 {
			quantity = amt
			posSide = side
		} else if amt < 0 {
			quantity = -amt
			posSide = side
		}
		if quantity > 0 {
			if ep, ok := pos["entryPrice"].(float64); ok && ep > 0 {
				entryPrice = ep
			}
			if mark, ok := pos["markPrice"].(float64); ok && entryPrice == 0 && mark > 0 {
				entryPrice = mark
			}
			break
		}
	}
	if quantity <= 0 || posSide == "" {
		return false
	}

	// ATR 移动止盈止损：仅更新内存状态，不挂交易所单；每档只触发一次，不重置 TriggeredStage
	if useATRTrailing {
		key := decision.Symbol + "_" + posSide
		stages := normalizeATRStages(decision.ATRTrailingTpStages)
		at.atrTrailingMu.Lock()
		if s, ok := at.atrTrailingState[key]; ok && s != nil {
			s.AtrSlMult = decision.ATRTrailingSlMult
			s.AtrTpMult = decision.ATRTrailingTpMult
			s.TpStages = stages
			if entryPrice > 0 {
				s.EntryPrice = entryPrice
			}
			if decision.StopLoss > 0 {
				s.AIStopLoss = decision.StopLoss // 与 TrailingSLPrice 并集，谁先触发听谁的
			}
			// 不覆盖 OriginalQty、不重置 TriggeredStage，避免 AI 调低倍数后重复触发同一档
			logger.Infof("  ✓ [updateTpSl] ATR trailing updated %s %s: sl=%.2f× tp=%.2f× stages=%d (triggered unchanged)", decision.Symbol, posSide, s.AtrSlMult, s.AtrTpMult, len(s.TpStages))
		} else {
			entryATR := at.getEntryATR(decision.Symbol)
			aiSL := 0.0
			if decision.StopLoss > 0 {
				aiSL = decision.StopLoss
			}
			at.atrTrailingState[key] = &ATRTrailingState{
				Symbol:           decision.Symbol,
				Side:             posSide,
				EntryPrice:       entryPrice,
				OriginalQty:      quantity,
				CurrentQty:       quantity,
				AtrSlMult:        decision.ATRTrailingSlMult,
				AtrTpMult:        decision.ATRTrailingTpMult,
				TpStages:         stages,
				EntryATR:         entryATR,
				FirstBatchClosed: false,
				AIStopLoss:       aiSL,
			}
			logger.Infof("  ✓ [updateTpSl] ATR trailing state created %s %s entry=%.4f origQty=%.4f entryATR=%.4f", decision.Symbol, posSide, entryPrice, quantity, entryATR)
		}
		at.atrTrailingMu.Unlock()
		// 不 return：若 AI 同时给出固定 stop_loss / take_profit / take_profit_stages，下面一并挂到交易所
	}

	sideUpper := strings.ToUpper(posSide)
	updateTP := decision.TakeProfit > 0 || len(decision.TakeProfitStages) > 0
	updateSL := decision.StopLoss > 0
	logger.Infof("  📍 [updateTpSl] %s 已有 %s 持仓 qty=%.4f，updateTP=%v updateSL=%v (TP=%.4f, stages=%d, SL=%.4f)",
		decision.Symbol, posSide, quantity, updateTP, updateSL, decision.TakeProfit, len(decision.TakeProfitStages), decision.StopLoss)
	// Only cancel and set TP when AI provided a new take_profit / take_profit_stages; otherwise keep existing TP (trailing stop = SL only).
	if updateTP {
		at.applyStaticTakeProfit(decision.Symbol, sideUpper, quantity, decision)
	} else {
		logger.Infof("  ✓ [updateTpSl] take_profit not set (≤0 or omitted), keeping existing TP order")
	}
	// Only cancel and set SL when AI provided a new stop_loss.
	if updateSL {
		if err := at.trader.CancelStopLossOrders(decision.Symbol); err != nil {
			logger.Infof("  ⚠️ [updateTpSl] cancel stop loss orders: %v", err)
		}
		if err := at.trader.SetStopLoss(decision.Symbol, sideUpper, quantity, decision.StopLoss); err != nil {
			logger.Infof("  ⚠️ [updateTpSl] set stop loss: %v", err)
		} else {
			logger.Infof("  ✓ [updateTpSl] stop loss set: %.4f", decision.StopLoss)
		}
	}
	return true
}

// applyStaticTakeProfit 使用交易所原生 TP 功能挂静态分批止盈：
// - 若 decision.TakeProfitStages 非空，则根据各档 close_pct 按当前持仓数量拆分多笔 TP 单；
// - 否则退回到单一 take_profit。
// 注意：这里的 close_pct 是针对“当前剩余仓位”的百分比，而不是开仓时的原始仓位。
func (at *AutoTrader) applyStaticTakeProfit(symbol, posSideUpper string, quantity float64, decision *kernel.Decision) {
	stages := at.normalizeStaticTakeProfitStages(decision.TakeProfitStages, posSideUpper)

	// 先撤掉原有 TP 单，避免旧档位残留
	if err := at.trader.CancelTakeProfitOrders(symbol); err != nil {
		logger.Infof("  ⚠️ [applyStaticTakeProfit] cancel take profit orders: %v", err)
	}

	// 若没有分档配置，则退回到单一 take_profit
	if len(stages) == 0 {
		if decision.TakeProfit <= 0 {
			logger.Infof("  ⚠️ [applyStaticTakeProfit] no valid take_profit_stages and take_profit ≤ 0, skip setting TP")
			return
		}
		if err := at.trader.SetTakeProfit(symbol, posSideUpper, quantity, decision.TakeProfit); err != nil {
			logger.Infof("  ⚠️ [applyStaticTakeProfit] set single take profit: %v", err)
		} else {
			logger.Infof("  ✓ [applyStaticTakeProfit] single take profit set: price=%.4f qty=%.4f", decision.TakeProfit, quantity)
		}
		return
	}

	remaining := quantity
	var assigned float64
	for idx, st := range stages {
		if remaining <= 0 {
			break
		}
		stageQty := quantity * (st.ClosePct / 100.0)
		// 最后一档用“剩余全部”兜底，避免浮点误差导致总和 < 100%
		if idx == len(stages)-1 {
			stageQty = quantity - assigned
		}
		if stageQty <= 0 {
			continue
		}
		if stageQty > remaining {
			stageQty = remaining
		}

		if err := at.trader.SetTakeProfit(symbol, posSideUpper, stageQty, st.Price); err != nil {
			logger.Infof("  ⚠️ [applyStaticTakeProfit] set TP stage %d failed: price=%.4f qty=%.4f (%.2f%% of %.4f): %v",
				idx+1, st.Price, stageQty, st.ClosePct, quantity, err)
		} else {
			logger.Infof("  ✓ [applyStaticTakeProfit] TP stage %d set: price=%.4f qty=%.4f (%.2f%% of %.4f)",
				idx+1, st.Price, stageQty, st.ClosePct, quantity)
		}

		assigned += stageQty
		remaining -= stageQty
	}
}

// normalizeStaticTakeProfitStages 过滤非法 close_pct/price，并根据多空方向做价格排序；最多保留 5 档，避免过多 TP 订单。
func (at *AutoTrader) normalizeStaticTakeProfitStages(stages []kernel.TakeProfitStage, posSideUpper string) []kernel.TakeProfitStage {
	if len(stages) == 0 {
		return nil
	}

	// 过滤非法值
	filtered := make([]kernel.TakeProfitStage, 0, len(stages))
	for _, st := range stages {
		if st.Price <= 0 {
			continue
		}
		if st.ClosePct <= 0 {
			continue
		}
		filtered = append(filtered, st)
	}
	if len(filtered) == 0 {
		return nil
	}

	// 排序：多头按 price 升序，空头按 price 降序
	isLong := strings.ToUpper(posSideUpper) == "LONG"
	sort.Slice(filtered, func(i, j int) bool {
		if isLong {
			return filtered[i].Price < filtered[j].Price
		}
		return filtered[i].Price > filtered[j].Price
	})

	// 限制最多 5 档，避免一次挂太多单
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}

	// 防止 close_pct 总和明显 > 100，在这里做一次缩放（验证层已经做过 ≤100 的硬校验，这里仅应对浮点误差）
	var sumPct float64
	for _, st := range filtered {
		sumPct += st.ClosePct
	}
	if sumPct > 100 && sumPct > 0 {
		scale := 100.0 / sumPct
		for i := range filtered {
			filtered[i].ClosePct *= scale
		}
	}

	return filtered
}

// ExecuteDecision executes a trading decision from external sources (e.g., debate consensus)
// This is a public method that can be called by other modules
func (at *AutoTrader) ExecuteDecision(d *kernel.Decision) error {
	logger.Infof("[%s] Executing external decision: %s %s", at.name, d.Action, d.Symbol)

	// Create a minimal action record for tracking
	actionRecord := &store.DecisionAction{
		Symbol:     d.Symbol,
		Action:     d.Action,
		Leverage:   d.Leverage,
		StopLoss:   d.StopLoss,
		TakeProfit: d.TakeProfit,
		Confidence: d.Confidence,
		Reasoning:  d.Reasoning,
	}

	// Execute the decision（外部决策无本轮 CoT，传空）
	err := at.executeDecisionWithRecord(d, actionRecord, "")
	if err != nil {
		logger.Errorf("[%s] External decision execution failed: %v", at.name, err)
		return err
	}

	logger.Infof("[%s] External decision executed successfully: %s %s", at.name, d.Action, d.Symbol)
	return nil
}

// executeOpenLongWithRecord executes open long position and records detailed information
// aiReasoning 为本轮 CoT，实盘会写入 pending_reasonings 供 OrderSync 创建仓位时填充 ai_reasoning_at_open
func (at *AutoTrader) executeOpenLongWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction, aiReasoning string) error {
	logger.Infof("  📈 Open long: %s", decision.Symbol)

	// ⚠️ Get current positions for multiple checks
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// [CODE ENFORCED] Check max positions limit
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}

	// Check if there's already a position in the same symbol and direction
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
			return fmt.Errorf("❌ %s already has long position, close it first", decision.Symbol)
		}
	}

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange, nil)
	if err != nil {
		return err
	}

	// Get balance (needed for multiple checks)
	balance, err := at.trader.GetBalance()
	if err != nil {
		return fmt.Errorf("failed to get account balance: %w", err)
	}
	availableBalance := 0.0
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Get equity for position value ratio check
	equity := 0.0
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		equity = eq
	} else if eq, ok := balance["totalWalletBalance"].(float64); ok && eq > 0 {
		equity = eq
	} else {
		equity = availableBalance // Fallback to available balance
	}

	// [CODE ENFORCED] Position Value Ratio Check: position_value <= equity × ratio
	adjustedPositionSize, wasCapped := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol)
	if wasCapped {
		decision.PositionSizeUSD = adjustedPositionSize
	}

	// ⚠️ Auto-adjust position size if insufficient margin
	// Formula: totalRequired = positionSize/leverage + positionSize*0.001 + positionSize/leverage*0.01
	//        = positionSize * (1.01/leverage + 0.001)
	marginFactor := 1.01/float64(decision.Leverage) + 0.001
	maxAffordablePositionSize := availableBalance / marginFactor

	actualPositionSize := decision.PositionSizeUSD
	if actualPositionSize > maxAffordablePositionSize {
		// Use 98% of max to leave buffer for price fluctuation
		adjustedSize := maxAffordablePositionSize * 0.98
		logger.Infof("  ⚠️ Position size %.2f exceeds max affordable %.2f, auto-reducing to %.2f",
			actualPositionSize, maxAffordablePositionSize, adjustedSize)
		actualPositionSize = adjustedSize
		decision.PositionSizeUSD = actualPositionSize
	}

	// [CODE ENFORCED] Minimum position size check
	if err := at.enforceMinPositionSize(decision.PositionSizeUSD); err != nil {
		return err
	}

	// Calculate quantity with adjusted position size
	quantity := actualPositionSize / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// Set margin mode
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Continue execution, doesn't affect trading
	}

	// Open position
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	logger.Infof("  ✓ Position opened successfully, order ID: %v, quantity: %.4f", order["orderId"], quantity)

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "open_long", quantity, marketData.CurrentPrice, decision.Leverage, 0)

	// 实盘决策记忆：将本轮 CoT 写入 pending_reasonings，OrderSync 创建 TraderPosition 时会自动填入 ai_reasoning_at_open
	if at.store != nil && aiReasoning != "" {
		if err := at.store.Position().AddPendingReasoning(at.id, decision.Symbol, "LONG", aiReasoning); err != nil {
			logger.Infof("  ⚠ Failed to add pending reasoning: %v", err)
		} else {
			logger.Debugf("[Memory] Successfully cached reasoning for %s %s", decision.Symbol, "LONG")
		}
	}

	// Record position opening time
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// ATR 移动止盈止损：可选；若 AI 给出 atr_sl_mult/atr_tp_mult/atr_tp_stages，则注册由机器狗按价格监控（含 1R 首批止盈用 EntryATR）
	if at.config.StrategyConfig != nil && at.config.StrategyConfig.Indicators.EnableATRTrailing &&
		(decision.ATRTrailingSlMult > 0 || decision.ATRTrailingTpMult > 0 || len(decision.ATRTrailingTpStages) > 0) {
		stages := normalizeATRStages(decision.ATRTrailingTpStages)
		entryATR := at.getEntryATR(decision.Symbol)
		key := decision.Symbol + "_long"
		at.atrTrailingMu.Lock()
		at.atrTrailingState[key] = &ATRTrailingState{
			Symbol:           decision.Symbol,
			Side:             "long",
			EntryPrice:       marketData.CurrentPrice,
			OriginalQty:      quantity,
			CurrentQty:       quantity,
			AtrSlMult:        decision.ATRTrailingSlMult,
			AtrTpMult:        decision.ATRTrailingTpMult,
			TpStages:         stages,
			EntryATR:         entryATR,
			FirstBatchClosed: false,
		}
		at.atrTrailingMu.Unlock()
		logger.Infof("  ✓ ATR trailing registered: sl=%.2f× tp=%.2f× stages=%d (origQty=%.4f, entryATR=%.4f)", decision.ATRTrailingSlMult, decision.ATRTrailingTpMult, len(stages), quantity, entryATR)
	}

	// 交易所固定止盈止损：与 ATR 不互斥；AI 若同时给出 stop_loss / take_profit / take_profit_stages，则一并挂到交易所（如硬止损/保底止盈）
	if decision.StopLoss > 0 {
		if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
			logger.Infof("  ⚠ Failed to set stop loss: %v", err)
		}
	}
	if decision.TakeProfit > 0 || len(decision.TakeProfitStages) > 0 {
		at.applyStaticTakeProfit(decision.Symbol, "LONG", quantity, decision)
	}

	return nil
}

// executeOpenShortWithRecord executes open short position and records detailed information
// aiReasoning 为本轮 CoT，实盘会写入 pending_reasonings 供 OrderSync 创建仓位时填充 ai_reasoning_at_open
func (at *AutoTrader) executeOpenShortWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction, aiReasoning string) error {
	logger.Infof("  📉 Open short: %s", decision.Symbol)

	// ⚠️ Get current positions for multiple checks
	positions, err := at.trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// [CODE ENFORCED] Check max positions limit
	if err := at.enforceMaxPositions(len(positions)); err != nil {
		return err
	}

	// Check if there's already a position in the same symbol and direction
	for _, pos := range positions {
		if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
			return fmt.Errorf("❌ %s already has short position, close it first", decision.Symbol)
		}
	}

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange, nil)
	if err != nil {
		return err
	}

	// Get balance (needed for multiple checks)
	balance, err := at.trader.GetBalance()
	if err != nil {
		return fmt.Errorf("failed to get account balance: %w", err)
	}
	availableBalance := 0.0
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Get equity for position value ratio check
	equity := 0.0
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		equity = eq
	} else if eq, ok := balance["totalWalletBalance"].(float64); ok && eq > 0 {
		equity = eq
	} else {
		equity = availableBalance // Fallback to available balance
	}

	// [CODE ENFORCED] Position Value Ratio Check: position_value <= equity × ratio
	adjustedPositionSize, wasCapped := at.enforcePositionValueRatio(decision.PositionSizeUSD, equity, decision.Symbol)
	if wasCapped {
		decision.PositionSizeUSD = adjustedPositionSize
	}

	// ⚠️ Auto-adjust position size if insufficient margin
	// Formula: totalRequired = positionSize/leverage + positionSize*0.001 + positionSize/leverage*0.01
	//        = positionSize * (1.01/leverage + 0.001)
	marginFactor := 1.01/float64(decision.Leverage) + 0.001
	maxAffordablePositionSize := availableBalance / marginFactor

	actualPositionSize := decision.PositionSizeUSD
	if actualPositionSize > maxAffordablePositionSize {
		// Use 98% of max to leave buffer for price fluctuation
		adjustedSize := maxAffordablePositionSize * 0.98
		logger.Infof("  ⚠️ Position size %.2f exceeds max affordable %.2f, auto-reducing to %.2f",
			actualPositionSize, maxAffordablePositionSize, adjustedSize)
		actualPositionSize = adjustedSize
		decision.PositionSizeUSD = actualPositionSize
	}

	// [CODE ENFORCED] Minimum position size check
	if err := at.enforceMinPositionSize(decision.PositionSizeUSD); err != nil {
		return err
	}

	// Calculate quantity with adjusted position size
	quantity := actualPositionSize / marketData.CurrentPrice
	actionRecord.Quantity = quantity
	actionRecord.Price = marketData.CurrentPrice

	// Set margin mode
	if err := at.trader.SetMarginMode(decision.Symbol, at.config.IsCrossMargin); err != nil {
		logger.Infof("  ⚠️ Failed to set margin mode: %v", err)
		// Continue execution, doesn't affect trading
	}

	// Open position
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	logger.Infof("  ✓ Position opened successfully, order ID: %v, quantity: %.4f", order["orderId"], quantity)

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "open_short", quantity, marketData.CurrentPrice, decision.Leverage, 0)

	// 实盘决策记忆：将本轮 CoT 写入 pending_reasonings，OrderSync 创建 TraderPosition 时会自动填入 ai_reasoning_at_open
	if at.store != nil && aiReasoning != "" {
		if err := at.store.Position().AddPendingReasoning(at.id, decision.Symbol, "SHORT", aiReasoning); err != nil {
			logger.Infof("  ⚠ Failed to add pending reasoning: %v", err)
		} else {
			logger.Debugf("[Memory] Successfully cached reasoning for %s %s", decision.Symbol, "SHORT")
		}
	}

	// Record position opening time
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()

	// ATR 移动止盈止损：可选；若 AI 给出 atr_sl_mult/atr_tp_mult/atr_tp_stages，则注册由机器狗按价格监控
	if at.config.StrategyConfig != nil && at.config.StrategyConfig.Indicators.EnableATRTrailing &&
		(decision.ATRTrailingSlMult > 0 || decision.ATRTrailingTpMult > 0 || len(decision.ATRTrailingTpStages) > 0) {
		stages := normalizeATRStages(decision.ATRTrailingTpStages)
		entryATR := at.getEntryATR(decision.Symbol)
		key := decision.Symbol + "_short"
		at.atrTrailingMu.Lock()
		at.atrTrailingState[key] = &ATRTrailingState{
			Symbol:           decision.Symbol,
			Side:             "short",
			EntryPrice:       marketData.CurrentPrice,
			OriginalQty:      quantity,
			CurrentQty:       quantity,
			AtrSlMult:        decision.ATRTrailingSlMult,
			AtrTpMult:        decision.ATRTrailingTpMult,
			TpStages:         stages,
			EntryATR:         entryATR,
			FirstBatchClosed: false,
		}
		at.atrTrailingMu.Unlock()
		logger.Infof("  ✓ ATR trailing registered: sl=%.2f× tp=%.2f× stages=%d (origQty=%.4f, entryATR=%.4f)", decision.ATRTrailingSlMult, decision.ATRTrailingTpMult, len(stages), quantity, entryATR)
	}

	// 交易所固定止盈止损：与 ATR 不互斥；AI 若同时给出 stop_loss / take_profit / take_profit_stages，则一并挂到交易所
	if decision.StopLoss > 0 {
		if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
			logger.Infof("  ⚠ Failed to set stop loss: %v", err)
		}
	}
	if decision.TakeProfit > 0 || len(decision.TakeProfitStages) > 0 {
		at.applyStaticTakeProfit(decision.Symbol, "SHORT", quantity, decision)
	}

	return nil
}

// executeCloseLongWithRecord executes close long position and records detailed information
func (at *AutoTrader) executeCloseLongWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 Close long: %s", decision.Symbol)

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange, nil)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// Normalize symbol for database lookup
	normalizedSymbol := market.Normalize(decision.Symbol)

	// Get entry price and quantity - prioritize local database for accurate quantity
	var entryPrice float64
	var quantity float64

	// First try to get from local database (more accurate for quantity)
	if at.store != nil {
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, normalizedSymbol, "LONG"); err == nil && openPos != nil {
			quantity = openPos.Quantity
			entryPrice = openPos.EntryPrice
			logger.Infof("  📊 Using local position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
		}
	}

	// Fallback to exchange API if local data not found
	if quantity == 0 {
		positions, err := at.trader.GetPositions()
		if err == nil {
			for _, pos := range positions {
				if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
					if ep, ok := pos["entryPrice"].(float64); ok {
						entryPrice = ep
					}
					if amt, ok := pos["positionAmt"].(float64); ok && amt > 0 {
						quantity = amt
					}
					break
				}
			}
		}
		logger.Infof("  📊 Using exchange position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
	}

	// Close quantity: 0 or omit = full close; quantity>0 = 按数量平仓; quantity_pct in (0,1] = 按比例平仓
	closeQty := quantity
	if decision.Quantity > 0 {
		if decision.Quantity < quantity {
			closeQty = decision.Quantity
			logger.Infof("  📉 Partial close (减仓): closing %.8f of %.8f", closeQty, quantity)
		}
	} else if decision.QuantityPct > 0 && decision.QuantityPct <= 1.0 {
		closeQty = quantity * decision.QuantityPct
		if closeQty < quantity {
			logger.Infof("  📉 Partial close by quantity_pct=%.2f: closing %.8f (%.0f%%) of %.8f", decision.QuantityPct, closeQty, decision.QuantityPct*100, quantity)
		}
	}

	// 下单前用交易所最新仓位做上限，避免与 1R 自动平 40% 等“同时开枪”导致 -2022 ReduceOnly
	actualQty, _ := at.getPositionQuantity(decision.Symbol, "long")
	if actualQty <= 0 {
		logger.Infof("  ✓ 仓位已无或已平，跳过 close_long (避免 -2022)")
		at.onATRFullClose(decision.Symbol, "long")
		return nil
	}
	if closeQty > actualQty {
		logger.Infof("  📌 计划平 %.8f，实际可平 %.8f，按实际可平数量下单（避免 -2022）", closeQty, actualQty)
		closeQty = actualQty
		quantity = actualQty
	}

	order, err := at.trader.CloseLong(decision.Symbol, closeQty)
	if err != nil {
		return err
	}

	if alreadyClosed, _ := order["alreadyClosed"].(bool); alreadyClosed {
		// 拦截后清理幽灵缓存：手动平仓导致 REST 返回已无仓位，必须立即从内存移除
		if bt, ok := at.trader.(*binance.FuturesTrader); ok {
			bt.ForceZeroPositionInCache(decision.Symbol, "LONG")
		}
		logger.Infof("  ✓ Position already closed, skipped (ghost cache cleared)")
		return nil
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "close_long", closeQty, marketData.CurrentPrice, 0, entryPrice)

	if closeQty >= quantity {
		at.onATRFullClose(decision.Symbol, "long")
	}
	logger.Infof("  ✓ Position closed successfully (qty=%.8f)", closeQty)
	return nil
}

// executeCloseShortWithRecord executes close short position and records detailed information
func (at *AutoTrader) executeCloseShortWithRecord(decision *kernel.Decision, actionRecord *store.DecisionAction) error {
	logger.Infof("  🔄 Close short: %s", decision.Symbol)

	// Get current price
	marketData, err := market.GetWithExchange(decision.Symbol, at.exchange, nil)
	if err != nil {
		return err
	}
	actionRecord.Price = marketData.CurrentPrice

	// Normalize symbol for database lookup
	normalizedSymbol := market.Normalize(decision.Symbol)

	// Get entry price and quantity - prioritize local database for accurate quantity
	var entryPrice float64
	var quantity float64

	// First try to get from local database (more accurate for quantity)
	if at.store != nil {
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, normalizedSymbol, "SHORT"); err == nil && openPos != nil {
			quantity = openPos.Quantity
			entryPrice = openPos.EntryPrice
			logger.Infof("  📊 Using local position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
		}
	}

	// Fallback to exchange API if local data not found
	if quantity == 0 {
		positions, err := at.trader.GetPositions()
		if err == nil {
			for _, pos := range positions {
				if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
					if ep, ok := pos["entryPrice"].(float64); ok {
						entryPrice = ep
					}
					if amt, ok := pos["positionAmt"].(float64); ok {
						quantity = -amt // positionAmt is negative for short
					}
					break
				}
			}
		}
		logger.Infof("  📊 Using exchange position data: qty=%.8f, entry=%.2f", quantity, entryPrice)
	}

	// Close quantity: 0 or omit = full close; quantity>0 = 按数量平仓; quantity_pct in (0,1] = 按比例平仓
	closeQty := quantity
	if decision.Quantity > 0 {
		if decision.Quantity < quantity {
			closeQty = decision.Quantity
			logger.Infof("  📉 Partial close (减仓): closing %.8f of %.8f", closeQty, quantity)
		}
	} else if decision.QuantityPct > 0 && decision.QuantityPct <= 1.0 {
		closeQty = quantity * decision.QuantityPct
		if closeQty < quantity {
			logger.Infof("  📉 Partial close by quantity_pct=%.2f: closing %.8f of %.8f", decision.QuantityPct, closeQty, quantity)
		}
	}

	// 下单前用交易所最新仓位做上限，避免与 1R 自动平 40% 等“同时开枪”导致 -2022 ReduceOnly
	actualQty, _ := at.getPositionQuantity(decision.Symbol, "short")
	if actualQty <= 0 {
		logger.Infof("  ✓ 仓位已无或已平，跳过 close_short (避免 -2022)")
		at.onATRFullClose(decision.Symbol, "short")
		return nil
	}
	if closeQty > actualQty {
		logger.Infof("  📌 计划平 %.8f，实际可平 %.8f，按实际可平数量下单（避免 -2022）", closeQty, actualQty)
		closeQty = actualQty
		quantity = actualQty
	}

	order, err := at.trader.CloseShort(decision.Symbol, closeQty)
	if err != nil {
		return err
	}

	if alreadyClosed, _ := order["alreadyClosed"].(bool); alreadyClosed {
		// 拦截后清理幽灵缓存：手动平仓导致 REST 返回已无仓位，必须立即从内存移除
		if bt, ok := at.trader.(*binance.FuturesTrader); ok {
			bt.ForceZeroPositionInCache(decision.Symbol, "SHORT")
		}
		logger.Infof("  ✓ Position already closed, skipped (ghost cache cleared)")
		return nil
	}

	// Record order ID
	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	}

	// Record order to database and poll for confirmation
	at.recordAndConfirmOrder(order, decision.Symbol, "close_short", closeQty, marketData.CurrentPrice, 0, entryPrice)

	if closeQty >= quantity {
		at.onATRFullClose(decision.Symbol, "short")
	}
	logger.Infof("  ✓ Position closed successfully (qty=%.8f)", closeQty)
	return nil
}

// GetID gets trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetUnderlyingTrader returns the underlying Trader interface implementation
// This is used by grid trading and other components that need direct exchange access
func (at *AutoTrader) GetUnderlyingTrader() Trader {
	return at.trader
}

// GetName gets trader name
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel gets AI model
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetExchange gets exchange
func (at *AutoTrader) GetExchange() string {
	return at.exchange
}

// GetShowInCompetition returns whether trader should be shown in competition
func (at *AutoTrader) GetShowInCompetition() bool {
	return at.showInCompetition
}

// SetShowInCompetition sets whether trader should be shown in competition
func (at *AutoTrader) SetShowInCompetition(show bool) {
	at.showInCompetition = show
}

// SetCustomPrompt sets custom trading strategy prompt
func (at *AutoTrader) SetCustomPrompt(prompt string) {
	at.customPrompt = prompt
}

// SetOverrideBasePrompt sets whether to override base prompt
func (at *AutoTrader) SetOverrideBasePrompt(override bool) {
	at.overrideBasePrompt = override
}

// GetSystemPromptTemplate gets current system prompt template name (from strategy config)
func (at *AutoTrader) GetSystemPromptTemplate() string {
	if at.strategyEngine != nil {
		config := at.strategyEngine.GetConfig()
		if config.CustomPrompt != "" {
			return "custom"
		}
	}
	return "strategy"
}

// saveEquitySnapshot saves equity snapshot independently (for drawing profit curve, decoupled from AI decision)
func (at *AutoTrader) saveEquitySnapshot(ctx *kernel.Context) {
	if at.store == nil || ctx == nil {
		return
	}

	snapshot := &store.EquitySnapshot{
		TraderID:      at.id,
		Timestamp:     time.Now().UTC(),
		TotalEquity:   ctx.Account.TotalEquity,
		Balance:       ctx.Account.TotalEquity - ctx.Account.UnrealizedPnL,
		UnrealizedPnL: ctx.Account.UnrealizedPnL,
		PositionCount: ctx.Account.PositionCount,
		MarginUsedPct: ctx.Account.MarginUsedPct,
	}

	if err := at.store.Equity().Save(snapshot); err != nil {
		logger.Infof("⚠️ Failed to save equity snapshot: %v", err)
	}
}

// saveDecision saves AI decision log to database (only records AI input/output, for debugging)
func (at *AutoTrader) saveDecision(record *store.DecisionRecord) error {
	if at.store == nil {
		return nil
	}

	at.cycleNumber++
	record.CycleNumber = at.cycleNumber
	record.TraderID = at.id

	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}

	if err := at.store.Decision().LogDecision(record); err != nil {
		logger.Infof("⚠️ Failed to save decision record: %v", err)
		return err
	}

	logger.Infof("📝 Decision record saved: trader=%s, cycle=%d", at.id, at.cycleNumber)
	return nil
}

// GetStore gets data store (for external access to decision records, etc.)
func (at *AutoTrader) GetStore() *store.Store {
	return at.store
}

// GetStatus gets system status (for API)
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	at.isRunningMutex.RLock()
	isRunning := at.isRunning
	at.isRunningMutex.RUnlock()

	result := map[string]interface{}{
		"trader_id":       at.id,
		"trader_name":     at.name,
		"ai_model":        at.aiModel,
		"exchange":        at.exchange,
		"is_running":      isRunning,
		"start_time":      at.startTime.Format(time.RFC3339),
		"runtime_minutes": int(time.Since(at.startTime).Minutes()),
		"call_count":      at.callCount,
		"initial_balance": at.initialBalance,
		"scan_interval":   at.config.ScanInterval.String(),
		"stop_until":      at.stopUntil.Format(time.RFC3339),
		"last_reset_time": at.lastResetTime.Format(time.RFC3339),
		"ai_provider":     aiProvider,
	}

	// Add strategy info
	if at.config.StrategyConfig != nil {
		result["strategy_type"] = at.config.StrategyConfig.StrategyType
		if at.config.StrategyConfig.GridConfig != nil {
			result["grid_symbol"] = at.config.StrategyConfig.GridConfig.Symbol
		}
	}

	return result
}

// GetAccountInfo gets account information (for API)
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	// Get account fields
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0
	totalEquity := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Use totalEquity directly if provided by trader (more accurate)
	if eq, ok := balance["totalEquity"].(float64); ok && eq > 0 {
		totalEquity = eq
	} else {
		// Fallback: Total Equity = Wallet balance + Unrealized profit
		totalEquity = totalWalletBalance + totalUnrealizedProfit
	}

	// Get positions to calculate total margin
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	totalMarginUsed := 0.0
	totalUnrealizedPnLCalculated := 0.0
	for _, pos := range positions {
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		totalUnrealizedPnLCalculated += unrealizedPnl

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed
	}

	// Verify unrealized P&L consistency (API value vs calculated from positions)
	// Note: Lighter API may return 0 for unrealized PnL, this is a known limitation
	diff := math.Abs(totalUnrealizedProfit - totalUnrealizedPnLCalculated)
	if diff > 5.0 { // Only warn if difference is significant (> 5 USDT)
		logger.Infof("⚠️ Unrealized P&L inconsistency (Lighter API limitation): API=%.4f, Calculated=%.4f, Diff=%.4f",
			totalUnrealizedProfit, totalUnrealizedPnLCalculated, diff)
	}

	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	} else {
		logger.Infof("⚠️ Initial Balance abnormal: %.2f, cannot calculate P&L percentage", at.initialBalance)
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return map[string]interface{}{
		// Core fields
		"total_equity":      totalEquity,           // Account equity = wallet + unrealized
		"wallet_balance":    totalWalletBalance,    // Wallet balance (excluding unrealized P&L)
		"unrealized_profit": totalUnrealizedProfit, // Unrealized P&L (official value from exchange API)
		"available_balance": availableBalance,      // Available balance

		// P&L statistics
		"total_pnl":       totalPnL,          // Total P&L = equity - initial
		"total_pnl_pct":   totalPnLPct,       // Total P&L percentage
		"initial_balance": at.initialBalance, // Initial balance
		"daily_pnl":       at.dailyPnL,       // Daily P&L

		// Position information
		"position_count":  len(positions),  // Position count
		"margin_used":     totalMarginUsed, // Margin used
		"margin_used_pct": marginUsedPct,   // Margin usage rate
	}, nil
}

// GetPositions gets position list (for API)
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		// Calculate margin used
		marginUsed := (quantity * markPrice) / float64(leverage)

		// Calculate P&L percentage (based on margin)
		pnlPct := calculatePnLPercentage(unrealizedPnl, marginUsed)

		result = append(result, map[string]interface{}{
			"symbol":             symbol,
			"side":               side,
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": pnlPct,
			"liquidation_price":  liquidationPrice,
			"margin_used":        marginUsed,
		})
	}

	return result, nil
}

// calculatePnLPercentage calculates P&L percentage (based on margin, automatically considers leverage)
// Return rate = Unrealized P&L / Margin × 100%
func calculatePnLPercentage(unrealizedPnl, marginUsed float64) float64 {
	if marginUsed > 0 {
		return (unrealizedPnl / marginUsed) * 100
	}
	return 0.0
}

// sortDecisionsByPriority sorts decisions: close positions first, then open positions, finally hold/wait
// This avoids position stacking overflow when changing positions
func sortDecisionsByPriority(decisions []kernel.Decision) []kernel.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// Define priority
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short":
			return 1 // Highest priority: close positions first
		case "open_long", "open_short":
			return 2 // Second priority: open positions later
		case "hold", "wait":
			return 3 // Lowest priority: wait
		default:
			return 999 // Unknown actions at the end
		}
	}

	// Copy decision list
	sorted := make([]kernel.Decision, len(decisions))
	copy(sorted, decisions)

	// Sort by priority
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	return sorted
}

// runPeakBottomWorker 在单独协程中处理 MFE/MAE 缓存更新并落库，不阻塞 runCycle
func (at *AutoTrader) runPeakBottomWorker() {
	defer at.monitorWg.Done()
	for {
		select {
		case <-at.stopMonitorCh:
			return
		case u := <-at.peakBottomCh:
			at.UpdatePeakPnL(u.Symbol, u.Side, u.PnlPct)
			at.UpdateBottomPnL(u.Symbol, u.Side, u.PnlPct)
			// 按 IsDryRun 落库：DryRun 更新 dry_run 仓位 MFE/MAE，实盘更新实盘表
			at.persistPeakBottomToDB(u.Symbol, u.Side)
		}
	}
}

// persistPeakBottomToDB 根据 IsDryRun 将当前极值写入 DB（dry_run 仓位或实盘仓位）
func (at *AutoTrader) persistPeakBottomToDB(symbol, side string) {
	if at.store == nil {
		return
	}
	normSymbol := market.Normalize(symbol)
	sideUpper := strings.ToUpper(side)
	var pos *store.TraderPosition
	var err error
	if at.config.IsDryRun {
		pos, err = at.store.Position().GetOpenPositionBySymbolAndSource(at.id, normSymbol, sideUpper, "dry_run")
	} else {
		pos, err = at.store.Position().GetOpenPositionBySymbol(at.id, normSymbol, sideUpper)
		if err == nil && pos != nil && pos.Source == "dry_run" {
			pos = nil
		}
	}
	if err != nil || pos == nil {
		return
	}
	price, _, ok := market.GetLatestPrice(normSymbol, at.exchange, 60*1000)
	if !ok || price <= 0 {
		return
	}
	if err := at.store.Position().UpdateMaxExcursionsFromPrice(pos, price); err != nil {
		return
	}
	logger.Debugf("✓ Updated peak PnL for %s %s: %.2f%%", normSymbol, side, at.peakPnLPctForLog(symbol, side))
}

// peakPnLPctForLog 仅用于日志展示，读缓存中的 peak PnL %
func (at *AutoTrader) peakPnLPctForLog(symbol, side string) float64 {
	posKey := symbol + "_" + strings.ToLower(side)
	at.peakPnLCacheMutex.RLock()
	defer at.peakPnLCacheMutex.RUnlock()
	return at.peakPnLCache[posKey]
}

// submitPeakBottomUpdate 非阻塞提交极值更新，供 runCycle/ DryRun 上下文使用
func (at *AutoTrader) submitPeakBottomUpdate(symbol, side string, pnlPct float64) {
	select {
	case at.peakBottomCh <- peakBottomUpdate{Symbol: symbol, Side: side, PnlPct: pnlPct}:
	default:
		// 队列满时丢弃，避免阻塞主循环
	}
}

// startDrawdownMonitor starts drawdown monitoring
func (at *AutoTrader) startDrawdownMonitor() {
	at.monitorWg.Add(1)
	go func() {
		defer at.monitorWg.Done()

		ticker := time.NewTicker(1 * time.Minute) // Check every minute
		defer ticker.Stop()

		logger.Info("📊 Started position drawdown monitoring (check every minute)")

		for {
			select {
			case <-ticker.C:
				at.checkPositionDrawdown()
			case <-at.stopMonitorCh:
				logger.Info("⏹ Stopped position drawdown monitoring")
				return
			}
		}
	}()
}

// checkPositionDrawdown checks position drawdown situation
func (at *AutoTrader) checkPositionDrawdown() {
	// 模拟盘：从 DB 取 dry_run 持仓，用当前价算浮盈并判断 TP/SL，触发时走 Dry-Run 平仓
	if at.config.IsDryRun {
		at.checkPositionDrawdownDryRun()
		return
	}

	// Get current positions
	positions, err := at.trader.GetPositions()
	if err != nil {
		logger.Infof("❌ Drawdown monitoring: failed to get positions: %v", err)
		return
	}

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := 0.0
		if mp, ok := pos["markPrice"].(float64); ok {
			markPrice = mp
		}
		if markPrice <= 0 {
			// 交易所/行情不可用时，回退热槽最后缓存价，不跳过极值对比
			if hot, _, ok := market.GetLatestPrice(market.Normalize(symbol), at.exchange, 5*60*1000); ok && hot > 0 {
				markPrice = hot
			}
		}
		if markPrice <= 0 {
			continue
		}
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // Short position quantity is negative, convert to positive
		}

		// Calculate current P&L percentage
		leverage := 10 // Default value
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		var currentPnLPct float64
		if side == "long" {
			currentPnLPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			currentPnLPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		// Construct unique position identifier (distinguish long/short)
		posKey := symbol + "_" + strings.ToLower(side)

		// Get historical peak profit for this position
		at.peakPnLCacheMutex.RLock()
		peakPnLPct, exists := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()

		if !exists {
			peakPnLPct = currentPnLPct
		}
		// 异步更新 MFE/MAE 缓存，不阻塞监控循环
		at.submitPeakBottomUpdate(symbol, side, currentPnLPct)

		// Calculate drawdown (magnitude of decline from peak)
		var drawdownPct float64
		if peakPnLPct > 0 && currentPnLPct < peakPnLPct {
			drawdownPct = ((peakPnLPct - currentPnLPct) / peakPnLPct) * 100
		}

		// 1. 绝对止损断头台 (硬性风控底线)
		const MaxAllowedLossPct = -30.0 // 最大允许亏损百分比 (带杠杆后的 PnL%)
		if currentPnLPct <= MaxAllowedLossPct {
			logger.Infof("🚨 [FATAL RISK] 触发物理断头台！%s %s 亏损达到 %.2f%%，无视 AI，立即强制平仓！", symbol, side, currentPnLPct)
			if err := at.emergencyClosePosition(symbol, side); err != nil {
				logger.Infof("❌ 断头台平仓失败 (%s %s): %v", symbol, side, err)
			} else {
				logger.Infof("✅ 断头台强制平仓成功: %s %s", symbol, side)
				at.ClearPeakPnLCache(symbol, side)
				at.ClearBottomPnLCache(symbol, side)
			}
			continue // 处理完毕，跳过该币种后续判断
		}

		// 2. 1R 首批止盈 + 0.8R 动态追踪止损（仅当策略开启 ATR 追踪且该仓位有 ATR 状态时执行，关闭 ATR 后不再追踪）
		var state *ATRTrailingState
		if at.config.StrategyConfig != nil && at.config.StrategyConfig.Indicators.EnableATRTrailing {
			at.atrTrailingMu.Lock()
			state = at.atrTrailingState[posKey]
			at.atrTrailingMu.Unlock()
		}
		if state != nil && state.EntryATR > 0 {
			isLong := side == "long"
			profitR := (markPrice - entryPrice) / state.EntryATR
			if !isLong {
				profitR = (entryPrice - markPrice) / state.EntryATR
			}
			// 2a. 1R 首批止盈：未执行过且利润达到 1R，平 40%
			if !state.FirstBatchClosed && profitR >= 1.0 {
				closeQty := quantity * 0.4
				if closeQty <= 0 {
					at.atrTrailingMu.Lock()
					state.FirstBatchClosed = true
					state.TrailingSLPrice = markPrice - 0.8*state.EntryATR
					if !isLong {
						state.TrailingSLPrice = markPrice + 0.8*state.EntryATR
					}
					at.atrTrailingMu.Unlock()
				} else {
					var err error
					if isLong {
						_, err = at.trader.CloseLong(symbol, closeQty)
					} else {
						_, err = at.trader.CloseShort(symbol, closeQty)
					}
					if err != nil {
						logger.Infof("❌ 1R 首批止盈平仓失败 (%s %s): %v", symbol, side, err)
					} else {
						logger.Infof("✅ 1R 首批止盈: %s %s 平仓 40%% (profitR=%.2f, qty=%.4f)", symbol, side, profitR, closeQty)
						at.atrTrailingMu.Lock()
						state.FirstBatchClosed = true
						state.TrailingSLPrice = markPrice - 0.8*state.EntryATR
						if !isLong {
							state.TrailingSLPrice = markPrice + 0.8*state.EntryATR
						}
						at.atrTrailingMu.Unlock()
						at.onATRPartialClose(symbol, side, closeQty, -1)
					}
				}
				continue
			}
			// 2b. 追踪止损与 AI 止损并集：取谁先触发听谁（多头取更高 SL，空头取更低 SL），防御力最高
			if state.FirstBatchClosed && state.TrailingSLPrice > 0 {
				if isLong {
					if markPrice > state.EntryPrice {
						newSL := markPrice - 0.8*state.EntryATR
						at.atrTrailingMu.Lock()
						if newSL > state.TrailingSLPrice {
							state.TrailingSLPrice = newSL
						}
						at.atrTrailingMu.Unlock()
					}
					effectiveSL := state.TrailingSLPrice
					if state.AIStopLoss > 0 && state.AIStopLoss > effectiveSL {
						effectiveSL = state.AIStopLoss
					}
					if markPrice <= effectiveSL {
						logger.Infof("🚨 追踪/AI 止损触发: %s %s 价格 %.4f <= 有效SL %.4f (Trailing=%.4f AI=%.4f)，全平", symbol, side, markPrice, effectiveSL, state.TrailingSLPrice, state.AIStopLoss)
						if err := at.emergencyClosePosition(symbol, side); err != nil {
							logger.Infof("❌ 追踪止损平仓失败 (%s %s): %v", symbol, side, err)
						} else {
							at.ClearPeakPnLCache(symbol, side)
							at.ClearBottomPnLCache(symbol, side)
						}
						continue
					}
				} else {
					if markPrice < state.EntryPrice {
						newSL := markPrice + 0.8*state.EntryATR
						at.atrTrailingMu.Lock()
						if newSL < state.TrailingSLPrice {
							state.TrailingSLPrice = newSL
						}
						at.atrTrailingMu.Unlock()
					}
					effectiveSL := state.TrailingSLPrice
					if state.AIStopLoss > 0 && state.AIStopLoss < effectiveSL {
						effectiveSL = state.AIStopLoss
					}
					if markPrice >= effectiveSL {
						logger.Infof("🚨 追踪/AI 止损触发: %s %s 价格 %.4f >= 有效SL %.4f (Trailing=%.4f AI=%.4f)，全平", symbol, side, markPrice, effectiveSL, state.TrailingSLPrice, state.AIStopLoss)
						if err := at.emergencyClosePosition(symbol, side); err != nil {
							logger.Infof("❌ 追踪止损平仓失败 (%s %s): %v", symbol, side, err)
						} else {
							at.ClearPeakPnLCache(symbol, side)
							at.ClearBottomPnLCache(symbol, side)
						}
						continue
					}
				}
			}
		}

		// Check close position condition: profit > 5% and drawdown >= 40%
		if currentPnLPct > 5.0 && drawdownPct >= 40.0 {
			logger.Infof("🚨 Drawdown close position condition triggered: %s %s | Current profit: %.2f%% | Peak profit: %.2f%% | Drawdown: %.2f%%",
				symbol, side, currentPnLPct, peakPnLPct, drawdownPct)

			// Execute close position
			if err := at.emergencyClosePosition(symbol, side); err != nil {
				logger.Infof("❌ Drawdown close position failed (%s %s): %v", symbol, side, err)
			} else {
				logger.Infof("✅ Drawdown close position succeeded: %s %s", symbol, side)
				at.ClearPeakPnLCache(symbol, side)
				at.ClearBottomPnLCache(symbol, side)
			}
		} else if currentPnLPct > 5.0 {
			// Record situations close to close position condition (for debugging)
			logger.Infof("📊 Drawdown monitoring: %s %s | Profit: %.2f%% | Peak: %.2f%% | Drawdown: %.2f%%",
				symbol, side, currentPnLPct, peakPnLPct, drawdownPct)
		}
	}
}

// checkPositionDrawdownDryRun 模拟盘护盘：从 DB 取 OPEN 的 dry_run 仓位，用当前价判断止盈止损并触发 Dry-Run 平仓
func (at *AutoTrader) checkPositionDrawdownDryRun() {
	if at.store == nil {
		return
	}
	openPositions, err := at.store.Position().GetOpenPositions(at.id)
	if err != nil {
		logger.Infof("❌ [Dry-Run] Drawdown: get open positions failed: %v", err)
		return
	}
	for _, pos := range openPositions {
		if pos.Source != "dry_run" {
			continue
		}
		symbol := pos.Symbol
		side := strings.ToLower(pos.Side)
		entryPrice := pos.EntryPrice
		leverage := pos.Leverage
		if leverage <= 0 {
			leverage = 10
		}

		markPrice := 0.0
		if data, err := market.GetWithExchange(symbol, at.exchange, nil); err == nil && data != nil {
			markPrice = data.CurrentPrice
		}
		if markPrice <= 0 {
			// CoinAnk/行情不可用时，强制回退热槽最后缓存价，不跳过极值对比
			if hot, _, ok := market.GetLatestPrice(market.Normalize(symbol), at.exchange, 5*60*1000); ok && hot > 0 {
				markPrice = hot
			}
		}
		if markPrice <= 0 {
			continue
		}

		var currentPnLPct float64
		if pos.Side == "LONG" {
			currentPnLPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			currentPnLPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		posKey := symbol + "_" + side
		at.peakPnLCacheMutex.RLock()
		peakPnLPct, exists := at.peakPnLCache[posKey]
		at.peakPnLCacheMutex.RUnlock()
		if !exists {
			peakPnLPct = currentPnLPct
		}
		at.submitPeakBottomUpdate(symbol, side, currentPnLPct)

		drawdownPct := 0.0
		if peakPnLPct > 0 && currentPnLPct < peakPnLPct {
			drawdownPct = ((peakPnLPct - currentPnLPct) / peakPnLPct) * 100
		}

		// 1. 断头台
		const MaxAllowedLossPct = -30.0
		if currentPnLPct <= MaxAllowedLossPct {
			logger.Infof("🚨 [Dry-Run] 断头台触发 %s %s 亏损 %.2f%%，强制平仓", symbol, side, currentPnLPct)
			if err := at.EmergencyClosePositionDryRun(symbol, side); err != nil {
				logger.Infof("❌ [Dry-Run] 断头台平仓失败 (%s %s): %v", symbol, side, err)
			} else {
				at.ClearPeakPnLCache(symbol, side)
				at.ClearBottomPnLCache(symbol, side)
			}
			continue
		}

		// 2. 回撤止盈：利润 > 5% 且回撤 >= 40%
		if currentPnLPct > 5.0 && drawdownPct >= 40.0 {
			logger.Infof("🚨 [Dry-Run] 回撤止盈触发: %s %s 利润 %.2f%% 回撤 %.2f%%", symbol, side, currentPnLPct, drawdownPct)
			if err := at.EmergencyClosePositionDryRun(symbol, side); err != nil {
				logger.Infof("❌ [Dry-Run] 回撤平仓失败 (%s %s): %v", symbol, side, err)
			} else {
				at.ClearPeakPnLCache(symbol, side)
				at.ClearBottomPnLCache(symbol, side)
			}
		}
	}
}

// EmergencyClosePositionDryRun 模拟盘强制平仓（供 API 调用）：不调用交易所，走 Dry-Run 引擎落库并更新 VirtualEquity
func (at *AutoTrader) EmergencyClosePositionDryRun(symbol, side string) error {
	decision := &kernel.Decision{Symbol: symbol, Action: ""}
	actionRecord := &store.DecisionAction{}
	if side == "long" {
		decision.Action = "close_long"
	} else {
		decision.Action = "close_short"
	}
	return at.executeDryRunOrder(decision, actionRecord, decision.Action, "")
}

// emergencyClosePosition emergency close position function (real exchange)
func (at *AutoTrader) emergencyClosePosition(symbol, side string) error {
	switch side {
	case "long":
		order, err := at.trader.CloseLong(symbol, 0) // 0 = close all
		if err != nil {
			return err
		}
		logger.Infof("✅ Emergency close long position succeeded, order ID: %v", order["orderId"])
	case "short":
		order, err := at.trader.CloseShort(symbol, 0) // 0 = close all
		if err != nil {
			return err
		}
		logger.Infof("✅ Emergency close short position succeeded, order ID: %v", order["orderId"])
	default:
		return fmt.Errorf("unknown position direction: %s", side)
	}
	// 清除 ATR 移动止盈止损状态，避免 watchdog 继续追踪已平仓位
	at.onATRFullClose(symbol, side)
	return nil
}

// GetPeakPnLCache gets peak profit cache
func (at *AutoTrader) GetPeakPnLCache() map[string]float64 {
	at.peakPnLCacheMutex.RLock()
	defer at.peakPnLCacheMutex.RUnlock()

	// Return a copy of the cache
	cache := make(map[string]float64)
	for k, v := range at.peakPnLCache {
		cache[k] = v
	}
	return cache
}

// UpdatePeakPnL updates peak profit cache
func (at *AutoTrader) UpdatePeakPnL(symbol, side string, currentPnLPct float64) {
	at.peakPnLCacheMutex.Lock()
	defer at.peakPnLCacheMutex.Unlock()

	posKey := symbol + "_" + strings.ToLower(side)
	if peak, exists := at.peakPnLCache[posKey]; exists {
		// Update peak (if long, take larger value; if short, currentPnLPct is negative, also compare)
		if currentPnLPct > peak {
			at.peakPnLCache[posKey] = currentPnLPct
		}
	} else {
		// First time recording
		at.peakPnLCache[posKey] = currentPnLPct
	}
}

// ClearPeakPnLCache clears peak cache for specified position
func (at *AutoTrader) ClearPeakPnLCache(symbol, side string) {
	at.peakPnLCacheMutex.Lock()
	defer at.peakPnLCacheMutex.Unlock()

	posKey := symbol + "_" + strings.ToLower(side)
	delete(at.peakPnLCache, posKey)
}

// UpdateBottomPnL updates bottom (min PnL / MAE) cache for drawdown and persistence
func (at *AutoTrader) UpdateBottomPnL(symbol, side string, currentPnLPct float64) {
	at.bottomPnLCacheMutex.Lock()
	defer at.bottomPnLCacheMutex.Unlock()

	posKey := symbol + "_" + strings.ToLower(side)
	if bottom, exists := at.bottomPnLCache[posKey]; exists {
		if currentPnLPct < bottom {
			at.bottomPnLCache[posKey] = currentPnLPct
		}
	} else {
		at.bottomPnLCache[posKey] = currentPnLPct
	}
}

// GetBottomPnLCache returns a copy of bottom PnL % cache (symbol_side -> min PnL %)
func (at *AutoTrader) GetBottomPnLCache() map[string]float64 {
	at.bottomPnLCacheMutex.RLock()
	defer at.bottomPnLCacheMutex.RUnlock()

	cache := make(map[string]float64)
	for k, v := range at.bottomPnLCache {
		cache[k] = v
	}
	return cache
}

// ClearBottomPnLCache clears bottom cache for specified position
func (at *AutoTrader) ClearBottomPnLCache(symbol, side string) {
	at.bottomPnLCacheMutex.Lock()
	defer at.bottomPnLCacheMutex.Unlock()

	posKey := symbol + "_" + strings.ToLower(side)
	delete(at.bottomPnLCache, posKey)
}

// recordAndConfirmOrder polls order status for actual fill data and records position
// action: open_long, open_short, close_long, close_short
// entryPrice: entry price when closing (0 when opening)
func (at *AutoTrader) recordAndConfirmOrder(orderResult map[string]interface{}, symbol, action string, quantity float64, price float64, leverage int, entryPrice float64) {
	if at.store == nil {
		return
	}

	// Get order ID (supports multiple types)
	var orderID string
	switch v := orderResult["orderId"].(type) {
	case int64:
		orderID = fmt.Sprintf("%d", v)
	case float64:
		orderID = fmt.Sprintf("%.0f", v)
	case string:
		orderID = v
	default:
		orderID = fmt.Sprintf("%v", v)
	}

	if orderID == "" || orderID == "0" {
		logger.Infof("  ⚠️ Order ID is empty, skipping record")
		return
	}

	// Determine positionSide
	var positionSide string
	switch action {
	case "open_long", "close_long":
		positionSide = "LONG"
	case "open_short", "close_short":
		positionSide = "SHORT"
	}

	var actualPrice = price
	var actualQty = quantity
	var fee float64

	// Exchanges with OrderSync: Skip immediate order recording, let OrderSync handle it
	// This ensures accurate data from GetTrades API and avoids duplicate records
	switch at.exchange {
	case "binance", "lighter", "hyperliquid", "bybit", "okx", "bitget", "aster", "kucoin", "gate":
		logger.Infof("  📝 Order submitted (id: %s), will be synced by OrderSync", orderID)
		return
	}

	// For exchanges without OrderSync (e.g., Binance): record immediately and poll for fill data
	orderRecord := at.createOrderRecord(orderID, symbol, action, positionSide, quantity, price, leverage)
	if err := at.store.Order().CreateOrder(orderRecord); err != nil {
		logger.Infof("  ⚠️ Failed to record order: %v", err)
	} else {
		logger.Infof("  📝 Order recorded: %s [%s] %s", orderID, action, symbol)
	}

	// Wait for order to be filled and get actual fill data
	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		status, err := at.trader.GetOrderStatus(symbol, orderID)
		if err == nil {
			statusStr, _ := status["status"].(string)
			if statusStr == "FILLED" {
				// Get actual fill price
				if avgPrice, ok := status["avgPrice"].(float64); ok && avgPrice > 0 {
					actualPrice = avgPrice
				}
				// Get actual executed quantity
				if execQty, ok := status["executedQty"].(float64); ok && execQty > 0 {
					actualQty = execQty
				}
				// Get commission/fee
				if commission, ok := status["commission"].(float64); ok {
					fee = commission
				}
				logger.Infof("  ✅ Order filled: avgPrice=%.6f, qty=%.6f, fee=%.6f", actualPrice, actualQty, fee)

				// Update order status to FILLED
				if err := at.store.Order().UpdateOrderStatus(orderRecord.ID, "FILLED", actualQty, actualPrice, fee); err != nil {
					logger.Infof("  ⚠️ Failed to update order status: %v", err)
				}

				// Record fill details
				at.recordOrderFill(orderRecord.ID, orderID, symbol, action, actualPrice, actualQty, fee)
				break
			} else if statusStr == "CANCELED" || statusStr == "EXPIRED" || statusStr == "REJECTED" {
				logger.Infof("  ⚠️ Order %s, skipping position record", statusStr)

				// Update order status
				if err := at.store.Order().UpdateOrderStatus(orderRecord.ID, statusStr, 0, 0, 0); err != nil {
					logger.Infof("  ⚠️ Failed to update order status: %v", err)
				}
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Normalize symbol for position record consistency
	normalizedSymbolForPosition := market.Normalize(symbol)

	logger.Infof("  📝 Recording position (ID: %s, action: %s, price: %.6f, qty: %.6f, fee: %.4f)",
		orderID, action, actualPrice, actualQty, fee)

	// Record position change with actual fill data (use normalized symbol)
	at.recordPositionChange(orderID, normalizedSymbolForPosition, positionSide, action, actualQty, actualPrice, leverage, entryPrice, fee)

	// Send anonymous trade statistics for experience improvement (async, non-blocking)
	// This helps us understand overall product usage across all deployments
	experience.TrackTrade(experience.TradeEvent{
		Exchange:  at.exchange,
		TradeType: action,
		Symbol:    symbol,
		AmountUSD: actualPrice * actualQty,
		Leverage:  leverage,
		UserID:    at.userID,
		TraderID:  at.id,
	})
}

// recordPositionChange records position change (create record on open, update record on close)
func (at *AutoTrader) recordPositionChange(orderID, symbol, side, action string, quantity, price float64, leverage int, entryPrice float64, fee float64) {
	if at.store == nil {
		return
	}

	switch action {
	case "open_long", "open_short":
		// Open position: create new position record
		nowMs := time.Now().UTC().UnixMilli()
		pos := &store.TraderPosition{
			TraderID:     at.id,
			ExchangeID:   at.exchangeID, // Exchange account UUID
			ExchangeType: at.exchange,   // Exchange type: binance/bybit/okx/etc
			Symbol:       symbol,
			Side:         side, // LONG or SHORT
			Quantity:     quantity,
			EntryPrice:   price,
			EntryOrderID: orderID,
			EntryTime:    nowMs,
			Leverage:     leverage,
			Status:       "OPEN",
			CreatedAt:    nowMs,
			UpdatedAt:    nowMs,
		}
		if err := at.store.Position().Create(pos); err != nil {
			logger.Infof("  ⚠️ Failed to record position: %v", err)
		} else {
			logger.Infof("  📊 Position recorded [%s] %s %s @ %.4f", at.id[:8], symbol, side, price)
		}

	case "close_long", "close_short":
		// MFE/MAE: 平仓前从内存极值缓存取出，赋给落盘对象，确保存库的是真实极值
		var mfe, mae float64
		if openPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, side); err == nil && openPos != nil {
			notional := openPos.EntryPrice * openPos.Quantity
			posKey := symbol + "_" + strings.ToLower(side)
			peakCache := at.GetPeakPnLCache()
			if peakPct, ok := peakCache[posKey]; ok {
				mfe = notional * (peakPct / 100)
			}
			bottomCache := at.GetBottomPnLCache()
			if bottomPct, ok := bottomCache[posKey]; ok {
				mae = notional * (bottomPct / 100)
			}
		}
		posBuilder := store.NewPositionBuilder(at.store.Position())
		if err := posBuilder.ProcessTrade(
			at.id, at.exchangeID, at.exchange,
			symbol, side, action,
			quantity, price, fee, 0, // realizedPnL will be calculated
			time.Now().UTC().UnixMilli(), orderID,
			mfe, mae,
			"", "", // entry/exit indicators not available in auto_trader path
		); err != nil {
			logger.Infof("  ⚠️ Failed to process close position: %v", err)
		} else {
			logger.Infof("  ✅ Position closed [%s] %s %s @ %.4f", at.id[:8], symbol, side, price)
		}
	}
}

// createOrderRecord creates an order record struct from order details
func (at *AutoTrader) createOrderRecord(orderID, symbol, action, positionSide string, quantity, price float64, leverage int) *store.TraderOrder {
	// Determine order type (market for auto trader)
	orderType := "MARKET"

	// Determine side (BUY/SELL)
	var side string
	switch action {
	case "open_long", "close_short":
		side = "BUY"
	case "open_short", "close_long":
		side = "SELL"
	}

	// Use action as orderAction directly (keep lowercase format)
	orderAction := action

	// Determine if it's a reduce only order
	reduceOnly := (action == "close_long" || action == "close_short")

	// Normalize symbol for consistency
	normalizedSymbol := market.Normalize(symbol)

	return &store.TraderOrder{
		TraderID:        at.id,
		ExchangeID:      at.exchangeID,
		ExchangeType:    at.exchange,
		ExchangeOrderID: orderID,
		Symbol:          normalizedSymbol,
		Side:            side,
		PositionSide:    positionSide,
		Type:            orderType,
		TimeInForce:     "GTC",
		Quantity:        quantity,
		Price:           price,
		Status:          "NEW",
		FilledQuantity:  0,
		AvgFillPrice:    0,
		Commission:      0,
		CommissionAsset: "USDT",
		Leverage:        leverage,
		ReduceOnly:      reduceOnly,
		ClosePosition:   reduceOnly,
		OrderAction:     orderAction,
		CreatedAt:       time.Now().UTC().UnixMilli(),
		UpdatedAt:       time.Now().UTC().UnixMilli(),
	}
}

// recordOrderFill records order fill/trade details
func (at *AutoTrader) recordOrderFill(orderRecordID int64, exchangeOrderID, symbol, action string, price, quantity, fee float64) {
	if at.store == nil {
		return
	}

	// Determine side (BUY/SELL)
	var side string
	switch action {
	case "open_long", "close_short":
		side = "BUY"
	case "open_short", "close_long":
		side = "SELL"
	}

	// Generate a simple trade ID (exchange doesn't always provide one)
	tradeID := fmt.Sprintf("%s-%d", exchangeOrderID, time.Now().UnixNano())

	// Normalize symbol for consistency
	normalizedSymbol := market.Normalize(symbol)

	fill := &store.TraderFill{
		TraderID:         at.id,
		ExchangeID:       at.exchangeID,
		ExchangeType:     at.exchange,
		OrderID:          orderRecordID,
		ExchangeOrderID:  exchangeOrderID,
		ExchangeTradeID:  tradeID,
		Symbol:           normalizedSymbol,
		Side:             side,
		Price:            price,
		Quantity:         quantity,
		QuoteQuantity:    price * quantity,
		Commission:       fee,
		CommissionAsset:  "USDT",
		RealizedPnL:      0, // Will be calculated for close orders
		IsMaker:          false, // Market orders are usually taker
		CreatedAt:        time.Now().UTC().UnixMilli(),
	}

	// Calculate realized PnL for close orders
	if action == "close_long" || action == "close_short" {
		// Try to get the entry price from the open position
		var positionSide string
		if action == "close_long" {
			positionSide = "LONG"
		} else {
			positionSide = "SHORT"
		}

		if openPos, err := at.store.Position().GetOpenPositionBySymbol(at.id, symbol, positionSide); err == nil && openPos != nil {
			if positionSide == "LONG" {
				fill.RealizedPnL = (price - openPos.EntryPrice) * quantity
			} else {
				fill.RealizedPnL = (openPos.EntryPrice - price) * quantity
			}
		}
	}

	if err := at.store.Order().CreateFill(fill); err != nil {
		logger.Infof("  ⚠️ Failed to record fill: %v", err)
	} else {
		logger.Infof("  📋 Fill recorded: %.4f @ %.6f, fee: %.4f", quantity, price, fee)
	}
}

// ============================================================================
// Risk Control Helpers
// ============================================================================

// isBTCETH checks if a symbol is BTC or ETH
func isBTCETH(symbol string) bool {
	symbol = strings.ToUpper(symbol)
	return strings.HasPrefix(symbol, "BTC") || strings.HasPrefix(symbol, "ETH")
}

// enforcePositionValueRatio checks and enforces position value ratio limits (CODE ENFORCED)
// Returns the adjusted position size (capped if necessary) and whether the position was capped
// positionSizeUSD: the original position size in USD
// equity: the account equity
// symbol: the trading symbol
func (at *AutoTrader) enforcePositionValueRatio(positionSizeUSD float64, equity float64, symbol string) (float64, bool) {
	if at.config.StrategyConfig == nil {
		return positionSizeUSD, false
	}

	riskControl := at.config.StrategyConfig.RiskControl

	// Get the appropriate position value ratio limit
	var maxPositionValueRatio float64
	if isBTCETH(symbol) {
		maxPositionValueRatio = riskControl.BTCETHMaxPositionValueRatio
		if maxPositionValueRatio <= 0 {
			maxPositionValueRatio = 5.0 // Default: 5x for BTC/ETH
		}
	} else {
		maxPositionValueRatio = riskControl.AltcoinMaxPositionValueRatio
		if maxPositionValueRatio <= 0 {
			maxPositionValueRatio = 1.0 // Default: 1x for altcoins
		}
	}

	// Calculate max allowed position value = equity × ratio
	maxPositionValue := equity * maxPositionValueRatio

	// Check if position size exceeds limit
	if positionSizeUSD > maxPositionValue {
		logger.Infof("  ⚠️ [RISK CONTROL] Position %.2f USDT exceeds limit (equity %.2f × %.1fx = %.2f USDT max for %s), capping",
			positionSizeUSD, equity, maxPositionValueRatio, maxPositionValue, symbol)
		return maxPositionValue, true
	}

	return positionSizeUSD, false
}

// enforceMinPositionSize checks minimum position size (CODE ENFORCED)
func (at *AutoTrader) enforceMinPositionSize(positionSizeUSD float64) error {
	if at.config.StrategyConfig == nil {
		return nil
	}

	minSize := at.config.StrategyConfig.RiskControl.MinPositionSize
	if minSize <= 0 {
		minSize = 12 // Default: 12 USDT
	}

	if positionSizeUSD < minSize {
		return fmt.Errorf("❌ [RISK CONTROL] Position %.2f USDT below minimum (%.2f USDT)", positionSizeUSD, minSize)
	}
	return nil
}

// enforceMaxPositions checks maximum positions count (CODE ENFORCED)
func (at *AutoTrader) enforceMaxPositions(currentPositionCount int) error {
	if at.config.StrategyConfig == nil {
		return nil
	}

	maxPositions := at.config.StrategyConfig.RiskControl.MaxPositions
	if maxPositions <= 0 {
		maxPositions = 3 // Default: 3 positions
	}

	if currentPositionCount >= maxPositions {
		return fmt.Errorf("❌ [RISK CONTROL] Already at max positions (%d/%d)", currentPositionCount, maxPositions)
	}
	return nil
}

// getSideFromAction converts order action to side (BUY/SELL)
func getSideFromAction(action string) string {
	switch action {
	case "open_long", "close_short":
		return "BUY"
	case "open_short", "close_long":
		return "SELL"
	default:
		return "BUY"
	}
}

// GetOpenOrders returns open orders (pending SL/TP) from exchange
func (at *AutoTrader) GetOpenOrders(symbol string) ([]OpenOrder, error) {
	return at.trader.GetOpenOrders(symbol)
}

