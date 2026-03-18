package market

import (
	"fmt"
	"sync/atomic"
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
	priceUpdateSubMu sync.RWMutex
	priceUpdateSubs  = make(map[uint64]chan PriceUpdateEvent)
	priceUpdateSeq   atomic.Uint64
)

const latestPriceKeyFmt = "%s|%s"

type PriceUpdateEvent struct {
	Symbol   string
	Exchange string
	Price    float64
	TimeMs   int64
}

// SetLatestPrice 由 WebSocket 线程调用，约 1ms 内完成写入，不涉及数据库或阻塞操作。
func SetLatestPrice(symbol, exchange string, price float64, timeMs int64) {
	symbol = Normalize(symbol)
	key := fmt.Sprintf(latestPriceKeyFmt, symbol, exchange)
	latestPriceStore.Store(key, &latestPriceSlot{Price: price, TimeMs: timeMs})
	broadcastPriceUpdate(PriceUpdateEvent{
		Symbol:   symbol,
		Exchange: exchange,
		Price:    price,
		TimeMs:   timeMs,
	})
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

func SubscribePriceUpdates(buffer int) (<-chan PriceUpdateEvent, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	ch := make(chan PriceUpdateEvent, buffer)
	id := priceUpdateSeq.Add(1)

	priceUpdateSubMu.Lock()
	priceUpdateSubs[id] = ch
	priceUpdateSubMu.Unlock()

	cancel := func() {
		priceUpdateSubMu.Lock()
		if sub, ok := priceUpdateSubs[id]; ok {
			delete(priceUpdateSubs, id)
			close(sub)
		}
		priceUpdateSubMu.Unlock()
	}

	return ch, cancel
}

func broadcastPriceUpdate(ev PriceUpdateEvent) {
	priceUpdateSubMu.RLock()
	defer priceUpdateSubMu.RUnlock()
	for _, ch := range priceUpdateSubs {
		select {
		case ch <- ev:
		default:
		}
	}
}
