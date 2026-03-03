package market

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"nofx/logger"
)

const (
	binanceTimeURL     = "https://api.binance.com/api/v3/time"
	serverTimeCacheTTL = 60 * time.Second
)

var (
	serverTimeOffsetMu sync.Mutex
	serverTimeOffsetMs int64
	serverTimeCachedAt time.Time
)

// getServerTimeOffsetMs 返回 (交易所服务器时间 - 本地时间) 的毫秒差，用于补偿本地时钟偏差。
// 若本地时钟比交易所快，offset 为负，effectiveNow = localNow + offset 会变小，避免误判 K 线为 stale。
// 结果缓存 60 秒，失败时返回 0（不补偿）。
func getServerTimeOffsetMs() int64 {
	serverTimeOffsetMu.Lock()
	defer serverTimeOffsetMu.Unlock()
	if time.Since(serverTimeCachedAt) < serverTimeCacheTTL {
		return serverTimeOffsetMs
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binanceTimeURL, nil)
	if err != nil {
		return serverTimeOffsetMs
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warnf("⚠️ Server time sync failed (clock offset unknown): %v", err)
		return serverTimeOffsetMs
	}
	defer resp.Body.Close()

	var body struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.ServerTime == 0 {
		return serverTimeOffsetMs
	}

	localMs := time.Now().UTC().UnixMilli()
	serverTimeOffsetMs = body.ServerTime - localMs
	serverTimeCachedAt = time.Now()
	if serverTimeOffsetMs > 1000 || serverTimeOffsetMs < -1000 {
		logger.Infof("🕐 Server time offset: %d ms (local %s)", serverTimeOffsetMs,
			time.UnixMilli(localMs).UTC().Format("15:04:05"))
	}
	return serverTimeOffsetMs
}
