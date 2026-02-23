package mcp

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"nofx/logger"
)

// Config client configuration (centralized management of all configurations)
type Config struct {
	// Provider configuration
	Provider string
	APIKey   string
	BaseURL  string
	Model    string

	// Behavior configuration
	MaxTokens   int
	Temperature float64
	UseFullURL  bool

	// Retry configuration
	MaxRetries     int
	RetryWaitBase  time.Duration
	RetryableErrors []string

	// Timeout configuration
	Timeout time.Duration

	// Dependency injection
	Logger     Logger
	HTTPClient *http.Client
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	// Long default (600s) for models with extended thinking (e.g. Claude); avoid unexpected EOF from client timeout
	timeout := getEnvDuration("AI_TIMEOUT_SECONDS", 600*time.Second)
	return &Config{
		// Default values
		MaxTokens:       getEnvInt("AI_MAX_TOKENS", 819200),
		Temperature:     MCPClientTemperature,
		MaxRetries:      MaxRetryTimes,
		RetryWaitBase:   2 * time.Second,
		Timeout:         timeout,
		RetryableErrors: retryableErrors,

		// Default dependencies (use global logger)
		Logger:     logger.NewMCPLogger(),
		HTTPClient: &http.Client{Timeout: timeout},
	}
}

// getEnvInt reads integer from environment variable, returns default value if failed
func getEnvInt(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

// getEnvString reads string from environment variable, returns default value if empty
func getEnvString(key string, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// getEnvDuration reads duration (in seconds) from environment variable, returns default value if failed
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return time.Duration(parsed) * time.Second
		}
	}
	return defaultValue
}
