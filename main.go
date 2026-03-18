package main

import (
	"nofx/api"
	"nofx/auth"
	"nofx/backtest"
	"nofx/config"
	"nofx/crypto"
	"nofx/experience"
	"nofx/logger"
	"nofx/manager"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env environment variables
	_ = godotenv.Load()

	// Initialize logger
	logger.Init(nil)

	logger.Info("╔════════════════════════════════════════════════════════════╗")
	logger.Info("║           🚀 NOFX - AI-Powered Trading System              ║")
	logger.Info("╚════════════════════════════════════════════════════════════╝")

	// Initialize global configuration (loaded from .env)
	config.Init()
	cfg := config.Get()
	logger.Info("✅ Configuration loaded")

	// Initialize encryption service BEFORE database (plaintext/base64 mode, no keys required)
	logger.Info("🔐 Initializing encryption service...")
	cryptoService, err := crypto.NewCryptoService()
	if err != nil {
		logger.Warnf("⚠️ Encryption service init failed: %v", err)
		cryptoService = nil
	} else {
		crypto.SetGlobalCryptoService(cryptoService)
		logger.Info("🔓 Encryption service disabled, using plaintext mode for local environment")
	}

	// Initialize database from configuration
	// For backward compatibility: command line arg overrides config (SQLite only)
	if len(os.Args) > 1 {
		cfg.DBPath = os.Args[1]
	}
	// Ensure data directory exists (for SQLite)
	if cfg.DBType == "sqlite" {
		if dir := filepath.Dir(cfg.DBPath); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				logger.Errorf("Failed to create data directory: %v", err)
			}
		}
	}

	logger.Infof("📋 Initializing database (%s)...", cfg.DBType)
	dbType := store.DBTypeSQLite
	if cfg.DBType == "postgres" {
		dbType = store.DBTypePostgres
	}
	st, err := store.NewWithConfig(store.DBConfig{
		Type:        dbType,
		DatabaseURL: cfg.DatabaseURL,
		Path:        cfg.DBPath,
		Host:        cfg.DBHost,
		Port:        cfg.DBPort,
		User:        cfg.DBUser,
		Password:    cfg.DBPassword,
		DBName:      cfg.DBName,
		SSLMode:     cfg.DBSSLMode,
	})
	if err != nil {
		logger.Fatalf("❌ Failed to initialize database: %v", err)
	}
	defer st.Close()
	backtest.UseDatabaseWithType(st.DB(), st.DBType() == store.DBTypePostgres)

	// Initialize installation ID for experience improvement (anonymous statistics)
	initInstallationID(st)

	// Set JWT secret
	auth.SetJWTSecret(cfg.JWTSecret)
	logger.Info("🔑 JWT secret configured")

	// K-line market data now uses CoinAnk + native exchange WebSocket streams with REST fallback.
	// WebSocket K-line streams are managed internally by market/stream_klines.go (no explicit WSMonitor).
	logger.Info("📊 Using CoinAnk K-line WebSocket + REST fallback for market data (streaming enabled)")

	// Create TraderManager and BacktestManager
	traderManager := manager.NewTraderManager()
	coachService := manager.NewCoachService(st, traderManager)
	mcpClient := newSharedMCPClient()
	backtestManager := backtest.NewManager(mcpClient)

	// Start streaming updater for position MFE/MAE: whenever K-line WebSocket has new prices,
	// update max_favorable_excursion / max_adverse_excursion in the database for all open positions.
	startPositionExcursionUpdater(st)
	if err := backtestManager.RestoreRuns(); err != nil {
		logger.Warnf("⚠️ Failed to restore backtest history: %v", err)
	}

	// Load all traders from database to memory (may auto-start traders with IsRunning=true)
	if err := traderManager.LoadTradersFromStore(st); err != nil {
		logger.Fatalf("❌ Failed to load traders: %v", err)
	}
	coachService.Start()
	defer coachService.Stop()

	// Display loaded trader information
	traders, err := st.Trader().List("default")
	if err != nil {
		logger.Fatalf("❌ Failed to get trader list: %v", err)
	}

	logger.Info("🤖 AI Trader Configurations in Database:")
	if len(traders) == 0 {
		logger.Info("  (No trader configurations, please create via Web interface)")
	} else {
		for _, t := range traders {
			status := "❌ Stopped"
			if t.IsRunning {
				status = "✅ Running"
			}
			logger.Infof("  • %s [%s] %s - AI Model: %s, Exchange: %s",
				t.Name, t.ID[:8], status, t.AIModelID, t.ExchangeID)
		}
	}

	// Start API server
	server := api.NewServer(traderManager, coachService, st, cryptoService, backtestManager, cfg.APIServerPort)
	go func() {
		if err := server.Start(); err != nil {
			logger.Fatalf("❌ Failed to start API server: %v", err)
		}
	}()

	// 实时量化筛查引擎（WebSocket K 线流 + 内存状态机）
	market.RunScreenerEngine()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("✅ System started successfully, waiting for trading commands...")
	logger.Info("📌 Tip: Use Ctrl+C to stop the system")

	<-quit
	logger.Info("📴 Shutdown signal received, closing system...")

	// Stop all traders
	traderManager.StopAll()
	logger.Info("✅ System shut down safely")
}

// startPositionExcursionUpdater listens to K-line WebSocket updates and updates
// max_favorable_excursion / max_adverse_excursion for all open positions whose
// symbol & exchange_type match the incoming ticker stream. This removes the need
// to rely solely on decision-time snapshots and keeps floating PnL extremes in
// the database as close to real-time as possible.
func startPositionExcursionUpdater(st *store.Store) {
	if st == nil {
		return
	}

	go func() {
		ch := market.SubscribeKlineUpdates()
		for ev := range ch {
			symbol := market.Normalize(ev.Symbol)
			exchange := strings.ToLower(strings.TrimSpace(ev.Exchange))
			interval := strings.TrimSpace(ev.Interval)
			if symbol == "" || interval == "" {
				continue
			}

			// Fetch latest price from K-line cache (with REST fallback when necessary).
			data, err := market.GetWithTimeframesWithExchange(symbol, []string{interval}, interval, nil, nil, exchange)
			if err != nil || data == nil || data.CurrentPrice <= 0 {
				continue
			}

			// Find all open positions for this symbol & exchange_type across traders.
			positions, err := st.Position().GetOpenPositionsBySymbolAndExchangeType(symbol, exchange)
			if err != nil {
				logger.Warnf("⚠️ Failed to load open positions for %s %s: %v", symbol, exchange, err)
				continue
			}
			if len(positions) == 0 {
				continue
			}

			for _, pos := range positions {
				if err := st.Position().UpdateMaxExcursionsFromPrice(pos, data.CurrentPrice); err != nil {
					logger.Warnf("⚠️ Failed to update MFE/MAE for %s %s: %v", pos.Symbol, pos.Side, err)
				}
			}
		}
	}()
}

// newSharedMCPClient creates a shared MCP AI client (for backtesting)
func newSharedMCPClient() mcp.AIClient {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		logger.Warn("⚠️ DEEPSEEK_API_KEY not set, AI features will be unavailable")
		return nil
	}
	return mcp.NewDeepSeekClient()
}

// initInstallationID initializes the anonymous installation ID for experience improvement
// This ID is persisted in database and used for anonymous usage statistics
func initInstallationID(st *store.Store) {
	const key = "installation_id"

	// Try to load from database
	installationID, err := st.GetSystemConfig(key)
	if err != nil {
		logger.Warnf("⚠️ Failed to load installation ID: %v", err)
	}

	// Generate new ID if not exists
	if installationID == "" {
		installationID = uuid.New().String()
		if err := st.SetSystemConfig(key, installationID); err != nil {
			logger.Warnf("⚠️ Failed to save installation ID: %v", err)
		}
		logger.Infof("📊 Generated new installation ID: %s", installationID[:8]+"...")
	}

	// Set installation ID in experience module
	experience.SetInstallationID(installationID)
}
