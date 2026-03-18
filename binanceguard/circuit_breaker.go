package binanceguard

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/logger"
)

const deepSilentDuration = 10 * time.Minute

var (
	circuitUntil         time.Time
	circuitMutex         sync.RWMutex
	bannedUntilRE        = regexp.MustCompile(`(?i)banned\s+until\s+(\d+)`)
	errCircuitBreakerOpen = fmt.Errorf("circuit breaker open: IP banned")
)

// CheckCircuitBreaker 在所有 Binance REST 调用前调用：若处于静默期则直接本地拦截。
func CheckCircuitBreaker() error {
	circuitMutex.RLock()
	until := circuitUntil
	circuitMutex.RUnlock()
	if until.IsZero() {
		return nil
	}
	now := time.Now()
	if now.Before(until) {
		return errCircuitBreakerOpen
	}

	circuitMutex.Lock()
	if time.Now().After(circuitUntil) {
		circuitUntil = time.Time{}
	}
	circuitMutex.Unlock()
	return nil
}

// SetCircuitBreakerFromError 识别 Binance -1003，并至少进入 10 分钟深度静默。
func SetCircuitBreakerFromError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if !strings.Contains(msg, "-1003") && !strings.Contains(msg, "1003") {
		return
	}

	until := time.Now().Add(deepSilentDuration)
	if matches := bannedUntilRE.FindStringSubmatch(msg); len(matches) >= 2 {
		if ms, parseErr := strconv.ParseInt(matches[1], 10, 64); parseErr == nil {
			parsed := time.UnixMilli(ms)
			if parsed.After(until) {
				until = parsed
			}
		}
	}

	circuitMutex.Lock()
	if until.After(circuitUntil) {
		circuitUntil = until
		logger.Errorf("[Binance] -1003 detected, deep silent until %s", until.UTC().Format("2006-01-02 15:04:05"))
	}
	circuitMutex.Unlock()
}

// IsCircuitBreakerOpen 供调用方判断是否因熔断返回。
func IsCircuitBreakerOpen(err error) bool {
	return err == errCircuitBreakerOpen
}

