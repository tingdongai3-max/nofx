package coinank_api

import (
	"context"
	"encoding/json"
	"net/http"
	"nofx/logger"
	"nofx/provider/coinank"
	"nofx/provider/coinank/coinank_enum"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	MainWsUrl      = "wss://ws.coinank.com/ws"
	wsPingInterval = 15 * time.Second
	wsReadDeadline = 90 * time.Second
	wsWriteWait    = 10 * time.Second
)

var wsDialer = websocket.Dialer{
	Proxy:            http.ProxyFromEnvironment,
	HandshakeTimeout: 10 * time.Second,
}

type KlineWs struct {
	conn      *websocket.Conn
	connMu    sync.Mutex
	KlineCh   <-chan *WsResult[coinank.KlineResult]
	TickersCh <-chan *WsResult[KlineTickers]
	closeCh   chan struct{}
	closeOnce sync.Once
}

// WsConn connects via gorilla/websocket with Ping/Pong keepalive and extended ReadDeadline.
func WsConn(ctx context.Context, needKline bool, needTicker bool) (*KlineWs, error) {
	conn, _, err := wsDialer.DialContext(ctx, MainWsUrl, nil)
	if err != nil {
		return nil, err
	}
	ch := make(chan string, 1024)
	closeCh := make(chan struct{})

	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))

	// Keep-alive: 每 15 秒发送 Ping 帧，防止心跳超时断连
	go func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-closeCh:
				return
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					logger.Warnf("⚠️ CoinAnk kline WS ping failed: %v", err)
					conn.Close()
					return
				}
			}
		}
	}()

	// Read loop
	go func() {
		defer conn.Close()
		defer close(ch)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.Warnf("⚠️ CoinAnk kline WS read error: %v", err)
				}
				return
			}
			conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
			select {
			case ch <- string(msg):
			case <-closeCh:
				return
			}
		}
	}()

	klineCh, tickersCh := handleResponse(ch, needKline, needTicker)
	return &KlineWs{
		conn:      conn,
		KlineCh:   klineCh,
		TickersCh: tickersCh,
		closeCh:   closeCh,
	}, nil
}

// Subscribe subscribe kline (single)
func (ws *KlineWs) Subscribe(symbol string, exchange coinank_enum.Exchange, interval coinank_enum.Interval) error {
	return ws.SubscribeBatch([]struct {
		Symbol   string
		Exchange coinank_enum.Exchange
		Interval coinank_enum.Interval
	}{{symbol, exchange, interval}})
}

// SubscribeBatch 批量订阅（多路复用：一个连接多个 topic）
func (ws *KlineWs) SubscribeBatch(items []struct {
	Symbol   string
	Exchange coinank_enum.Exchange
	Interval coinank_enum.Interval
}) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]string, 0, len(items))
	for _, it := range items {
		args = append(args, "kline@"+it.Symbol+"@"+string(it.Exchange)+"@"+string(it.Interval))
	}
	info := SubscribeInfoBatch{
		Op:   "subscribe",
		Args: args,
	}
	body, err := json.Marshal(info)
	if err != nil {
		return err
	}
	ws.connMu.Lock()
	ws.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	err = ws.conn.WriteMessage(websocket.TextMessage, body)
	ws.connMu.Unlock()
	return err
}

// UnSubscribe unsubscribe kline
func (ws *KlineWs) UnSubscribe(symbol string, exchange coinank_enum.Exchange, interval coinank_enum.Interval) error {
	args := "kline@" + symbol + "@" + string(exchange) + "@" + string(interval)
	info := SubscribeInfo{
		Op:   "unsubscribe",
		Args: args,
	}
	body, err := json.Marshal(info)
	if err != nil {
		return err
	}
	ws.connMu.Lock()
	ws.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	err = ws.conn.WriteMessage(websocket.TextMessage, body)
	ws.connMu.Unlock()
	return err
}

// Close websocket
func (ws *KlineWs) Close() error {
	var err error
	ws.closeOnce.Do(func() {
		close(ws.closeCh)
		ws.connMu.Lock()
		err = ws.conn.Close()
		ws.connMu.Unlock()
	})
	return err
}

type SubscribeInfo struct {
	Op   string `json:"op"`
	Args string `json:"args"`
}

type SubscribeInfoBatch struct {
	Op   string   `json:"op"`
	Args []string `json:"args"`
}

type KlineTickers struct {
	OiCcy          string `json:"oiCcy"`
	OiVol          string `json:"oiVol"`
	Symbol         string `json:"symbol"`
	ExchangeName   string `json:"exchangeName"`
	PriceChange24H string `json:"priceChange24h"`
	Low24H         string `json:"low24h"`
	High24H        string `json:"high24h"`
	VolCcy24H      string `json:"volCcy24h"`
	LastPrice      string `json:"lastPrice"`
	Vol24H         string `json:"vol24h"`
	Turnover24H    string `json:"turnover24h"`
	OiUSD          string `json:"oiUSD"`
	FundingRate    string `json:"fundingRate"`
	LastOiVol      string `json:"lastOiVol"`
	MarkPrice      string `json:"markPrice"`
	BasisRate      string `json:"basisRate"`
	Basis          string `json:"basis"`
}

type WsResult[T any] struct {
	Op      string `json:"op"`
	Success bool   `json:"success"`
	Args    string `json:"args"`
	Data    T      `json:"data"`
}

func handleResponse(ch <-chan string, needKline bool, needTicker bool) (<-chan *WsResult[coinank.KlineResult], <-chan *WsResult[KlineTickers]) {
	klineCh := make(chan *WsResult[coinank.KlineResult], 1024)
	tickersCh := make(chan *WsResult[KlineTickers], 1024)
	go func() {
		if needKline {
			defer close(klineCh)
		} else {
			close(klineCh)
		}
		if needTicker {
			defer close(tickersCh)
		} else {
			close(tickersCh)
		}
		for msg := range ch {
			if needKline && strings.HasPrefix(msg, "{\"op\":\"push\",\"success\":true,\"args\":\"kline") {
				var result WsResult[[]any]
				err := json.Unmarshal([]byte(msg), &result)
				if err == nil && result.Success {
					kline := coinank.KlineResult{}
					k := result.Data
					kline.StartTime = toInt64(k[0])
					kline.EndTime = toInt64(k[1])
					kline.Open = toFloat64(k[2])
					kline.Close = toFloat64(k[3])
					kline.High = toFloat64(k[4])
					kline.Low = toFloat64(k[5])
					kline.Volume = toFloat64(k[6])
					kline.Quantity = toFloat64(k[7])
					kline.Count = toFloat64(k[8])
					var resp WsResult[coinank.KlineResult]
					resp.Success = result.Success
					resp.Data = kline
					resp.Args = result.Args
					resp.Op = result.Op
					klineCh <- &resp
				}
			} else if needTicker && strings.HasPrefix(msg, "{\"op\":\"push\",\"success\":true,\"args\":\"tickers") {
				var result WsResult[KlineTickers]
				err := json.Unmarshal([]byte(msg), &result)
				if err == nil && result.Success {
					tickersCh <- &result
				}
			}
		}
	}()
	return klineCh, tickersCh
}

func toInt64(v any) int64 {
	f := toFloat64(v)
	return int64(f)
}

func toFloat64(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	if f, ok := v.(string); ok {
		s, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return 0
		}
		return s
	}
	return 0
}
