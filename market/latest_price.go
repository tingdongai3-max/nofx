package market

import (
	"fmt"
	"sync"
	"time"
)

// latestPriceSlot 无锁热槽：仅存最新价与时间戳，WebSocket 线程仅做内存覆盖，不等待任何 DB。
type latestPriceSlot struct {
	Price  float64
	TimeMs int64
}

var (
	latestPriceStore sync.Map // key: "symbol|exchange", value: *latestPriceSlot
)

const latestPriceKeyFmt = "%s|%s"

// SetLatestPrice 由 WebSocket 线程调用，约 1ms 内完成写入，不涉及数据库或阻塞操作。
func SetLatestPrice(symbol, exchange string, price float64, timeMs int64) {
	key := fmt.Sprintf(latestPriceKeyFmt, Normalize(symbol), exchange)
	latestPriceStore.Store(key, &latestPriceSlot{Price: price, TimeMs: timeMs})
}

// GetLatestPrice 读取热槽中的最新价；ok 表示存在且有效。
// maxAgeMs：若 (now - timeMs) > maxAgeMs 则视为过期，调用方可用 0 表示不检查年龄。
func GetLatestPrice(symbol, exchange string, maxAgeMs int64) (price float64, timeMs int64, ok bool) {
	key := fmt.Sprintf(latestPriceKeyFmt, Normalize(symbol), exchange)
	v, ok := latestPriceStore.Load(key)
	if !ok || v == nil {
		return 0, 0, false
	}
	slot := v.(*latestPriceSlot)
	if maxAgeMs > 0 && (time.Now().UTC().UnixMilli()-slot.TimeMs) > maxAgeMs {
		return slot.Price, slot.TimeMs, false
	}
	return slot.Price, slot.TimeMs, true
}
