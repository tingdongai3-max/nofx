package binance

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nofx/logger"
)

var (
	binanceCircuitUntil   time.Time
	binanceCircuitMutex   sync.RWMutex
	binanceBannedUntilRE  = regexp.MustCompile(`(?i)banned\s+until\s+(\d+)`)
	errCircuitBreakerOpen = fmt.Errorf("circuit breaker open: IP banned")
)

// CheckCircuitBreaker 在所有 REST 调用前调用：若处于封禁期则直接返回错误，禁止发请求
func CheckCircuitBreaker() error {
	binanceCircuitMutex.RLock()
	until := binanceCircuitUntil
	binanceCircuitMutex.RUnlock()
	if until.IsZero() {
		return nil
	}
	if time.Now().Before(until) {
		return errCircuitBreakerOpen
	}
	// 已过解封时间，清除状态
	binanceCircuitMutex.Lock()
	if time.Now().After(binanceCircuitUntil) {
		binanceCircuitUntil = time.Time{}
	}
	binanceCircuitMutex.Unlock()
	return nil
}

// SetCircuitBreakerFromError 收到 API 错误后调用：若为 -1003 且含 banned until 则解析并设置解封时间
func SetCircuitBreakerFromError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if !strings.Contains(msg, "-1003") && !strings.Contains(msg, "1003") {
		return
	}
	if !strings.Contains(strings.ToLower(msg), "banned until") {
		return
	}
	matches := binanceBannedUntilRE.FindStringSubmatch(msg)
	if len(matches) < 2 {
		return
	}
	ms, errParse := strconv.ParseInt(matches[1], 10, 64)
	if errParse != nil {
		return
	}
	until := time.UnixMilli(ms)
	// 若时间已过则忽略
	if until.Before(time.Now()) {
		return
	}
	binanceCircuitMutex.Lock()
	if until.After(binanceCircuitUntil) {
		binanceCircuitUntil = until
		logger.Errorf("[Binance] -1003 IP banned, circuit breaker open until %s", until.UTC().Format("2006-01-02 15:04:05"))
	}
	binanceCircuitMutex.Unlock()
}

// IsCircuitBreakerOpen 供调用方判断是否因熔断返回
func IsCircuitBreakerOpen(err error) bool {
	return err == errCircuitBreakerOpen
}
