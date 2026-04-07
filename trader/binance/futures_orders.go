package binance

import (
	"context"
	"fmt"
	"nofx/logger"
	"nofx/trader/types"
	"strconv"
	"strings"

	"github.com/adshao/go-binance/v2/futures"
)

// OpenLong opens a long position
func (t *FuturesTrader) OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return t.submitMarketEntryOrder(symbol, quantity, leverage, futures.SideTypeBuy, futures.PositionSideTypeLong, true)
}

// OpenShort opens a short position
func (t *FuturesTrader) OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return t.submitMarketEntryOrder(symbol, quantity, leverage, futures.SideTypeSell, futures.PositionSideTypeShort, true)
}

// AddLong submits a same-symbol add-position market order without canceling existing protection orders.
func (t *FuturesTrader) AddLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return t.submitMarketEntryOrder(symbol, quantity, leverage, futures.SideTypeBuy, futures.PositionSideTypeLong, false)
}

// AddShort submits a same-symbol add-position market order without canceling existing protection orders.
func (t *FuturesTrader) AddShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error) {
	return t.submitMarketEntryOrder(symbol, quantity, leverage, futures.SideTypeSell, futures.PositionSideTypeShort, false)
}

// CloseLong closes a long position
func (t *FuturesTrader) CloseLong(symbol string, quantity float64) (map[string]interface{}, error) {
	// If quantity is 0, get current position quantity
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "long" {
				quantity = pos["positionAmt"].(float64)
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("no long position found for %s", symbol)
		}
	}

	// Format quantity
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// Create market sell order (close long, using br ID)
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeSell).
		PositionSide(futures.PositionSideTypeLong).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("failed to close long position: %w", err)
	}

	logger.Infof("✓ Closed long position successfully: %s quantity: %s", symbol, quantityStr)

	// After closing position, cancel all pending orders for this symbol (stop-loss and take-profit orders)
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel pending orders: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// CloseShort closes a short position
func (t *FuturesTrader) CloseShort(symbol string, quantity float64) (map[string]interface{}, error) {
	// If quantity is 0, get current position quantity
	if quantity == 0 {
		positions, err := t.GetPositions()
		if err != nil {
			return nil, err
		}

		for _, pos := range positions {
			if pos["symbol"] == symbol && pos["side"] == "short" {
				quantity = -pos["positionAmt"].(float64) // Short position quantity is negative, take absolute value
				break
			}
		}

		if quantity == 0 {
			return nil, fmt.Errorf("no short position found for %s", symbol)
		}
	}

	// Format quantity
	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	// Create market buy order (close short, using br ID)
	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(futures.SideTypeBuy).
		PositionSide(futures.PositionSideTypeShort).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("failed to close short position: %w", err)
	}

	logger.Infof("✓ Closed short position successfully: %s quantity: %s", symbol, quantityStr)

	// After closing position, cancel all pending orders for this symbol (stop-loss and take-profit orders)
	if err := t.CancelAllOrders(symbol); err != nil {
		logger.Infof("  ⚠ Failed to cancel pending orders: %v", err)
	}

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

func (t *FuturesTrader) submitMarketEntryOrder(symbol string, quantity float64, leverage int, side futures.SideType, positionSide futures.PositionSideType, cancelWorkingOrders bool) (map[string]interface{}, error) {
	if cancelWorkingOrders {
		if err := t.CancelAllOrders(symbol); err != nil {
			logger.Infof("  ⚠ Failed to cancel old pending orders (may not have any): %v", err)
		}
	}

	if err := t.SetLeverage(symbol, leverage); err != nil {
		return nil, err
	}

	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	quantityFloat, parseErr := strconv.ParseFloat(quantityStr, 64)
	if parseErr != nil || quantityFloat <= 0 {
		return nil, fmt.Errorf("position size too small, rounded to 0 (original: %.8f → formatted: %s). Suggest increasing position amount or selecting a lower-priced coin", quantity, quantityStr)
	}

	if err := t.CheckMinNotional(symbol, quantityFloat); err != nil {
		return nil, err
	}

	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(positionSide).
		Type(futures.OrderTypeMarket).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to submit market order: %w", err)
	}

	actionLabel := "add"
	if cancelWorkingOrders {
		actionLabel = "open"
	}
	logger.Infof("✓ Submitted %s %s position successfully: %s quantity: %s", actionLabel, strings.ToLower(string(positionSide)), symbol, quantityStr)
	logger.Infof("  Order ID: %d", order.OrderID)

	result := make(map[string]interface{})
	result["orderId"] = order.OrderID
	result["clientOrderId"] = order.ClientOrderID
	result["symbol"] = order.Symbol
	result["status"] = order.Status
	return result, nil
}

// ReduceLong submits a reduce-only market order that trims a long position without canceling other orders.
func (t *FuturesTrader) ReduceLong(symbol string, quantity float64) (map[string]interface{}, error) {
	return t.submitReduceOnlyMarketOrder(symbol, futures.SideTypeSell, futures.PositionSideTypeLong, quantity)
}

// ReduceShort submits a reduce-only market order that trims a short position without canceling other orders.
func (t *FuturesTrader) ReduceShort(symbol string, quantity float64) (map[string]interface{}, error) {
	return t.submitReduceOnlyMarketOrder(symbol, futures.SideTypeBuy, futures.PositionSideTypeShort, quantity)
}

// CancelStopLossOrders cancels only stop-loss orders (doesn't affect take-profit orders)
// Now uses both legacy API and new Algo Order API
func (t *FuturesTrader) CancelStopLossOrders(symbol string) error {
	canceledCount := 0
	var cancelErrors []error

	// 1. Cancel legacy stop-loss orders
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, order := range orders {
			orderType := string(order.Type)

			// Only cancel stop-loss orders (don't cancel take-profit orders)
			// Use string comparison since OrderType constants were removed in v2.8.9
			if orderType == "STOP_MARKET" || orderType == "STOP" {
				_, err := t.client.NewCancelOrderService().
					Symbol(symbol).
					OrderID(order.OrderID).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Order ID %d: %v", order.OrderID, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel legacy stop-loss order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled legacy stop-loss order (Order ID: %d, Type: %s, Side: %s)", order.OrderID, orderType, order.PositionSide)
			}
		}
	}

	// 2. Cancel Algo stop-loss orders
	algoOrders, err := t.client.NewListOpenAlgoOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, algoOrder := range algoOrders {
			// Only cancel stop-loss orders
			if algoOrder.OrderType == futures.AlgoOrderTypeStopMarket || algoOrder.OrderType == futures.AlgoOrderTypeStop {
				_, err := t.client.NewCancelAlgoOrderService().
					AlgoID(algoOrder.AlgoId).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Algo ID %d: %v", algoOrder.AlgoId, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel Algo stop-loss order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled Algo stop-loss order (Algo ID: %d, Type: %s)", algoOrder.AlgoId, algoOrder.OrderType)
			}
		}
	}

	if canceledCount == 0 && len(cancelErrors) == 0 {
		logger.Infof("  ℹ %s has no stop-loss orders to cancel", symbol)
	} else if canceledCount > 0 {
		logger.Infof("  ✓ Canceled %d stop-loss order(s) for %s", canceledCount, symbol)
	}

	// If all cancellations failed, return error
	if len(cancelErrors) > 0 && canceledCount == 0 {
		return fmt.Errorf("failed to cancel stop-loss orders: %v", cancelErrors)
	}

	return nil
}

// CancelTakeProfitOrders cancels only take-profit orders (doesn't affect stop-loss orders)
// Now uses both legacy API and new Algo Order API
func (t *FuturesTrader) CancelTakeProfitOrders(symbol string) error {
	canceledCount := 0
	var cancelErrors []error

	// 1. Cancel legacy take-profit orders
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, order := range orders {
			orderType := string(order.Type)

			// Only cancel take-profit orders (don't cancel stop-loss orders)
			// Use string comparison since OrderType constants were removed in v2.8.9
			if orderType == "TAKE_PROFIT_MARKET" || orderType == "TAKE_PROFIT" {
				_, err := t.client.NewCancelOrderService().
					Symbol(symbol).
					OrderID(order.OrderID).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Order ID %d: %v", order.OrderID, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel legacy take-profit order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled legacy take-profit order (Order ID: %d, Type: %s, Side: %s)", order.OrderID, orderType, order.PositionSide)
			}
		}
	}

	// 2. Cancel Algo take-profit orders
	algoOrders, err := t.client.NewListOpenAlgoOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, algoOrder := range algoOrders {
			// Only cancel take-profit orders
			if algoOrder.OrderType == futures.AlgoOrderTypeTakeProfitMarket || algoOrder.OrderType == futures.AlgoOrderTypeTakeProfit {
				_, err := t.client.NewCancelAlgoOrderService().
					AlgoID(algoOrder.AlgoId).
					Do(context.Background())

				if err != nil {
					errMsg := fmt.Sprintf("Algo ID %d: %v", algoOrder.AlgoId, err)
					cancelErrors = append(cancelErrors, fmt.Errorf("%s", errMsg))
					logger.Infof("  ⚠ Failed to cancel Algo take-profit order: %s", errMsg)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled Algo take-profit order (Algo ID: %d, Type: %s)", algoOrder.AlgoId, algoOrder.OrderType)
			}
		}
	}

	if canceledCount == 0 && len(cancelErrors) == 0 {
		logger.Infof("  ℹ %s has no take-profit orders to cancel", symbol)
	} else if canceledCount > 0 {
		logger.Infof("  ✓ Canceled %d take-profit order(s) for %s", canceledCount, symbol)
	}

	// If all cancellations failed, return error
	if len(cancelErrors) > 0 && canceledCount == 0 {
		return fmt.Errorf("failed to cancel take-profit orders: %v", cancelErrors)
	}

	return nil
}

// CancelAllOrders cancels all pending orders for this symbol
// Now uses both legacy API and new Algo Order API
func (t *FuturesTrader) CancelAllOrders(symbol string) error {
	// 1. Cancel all legacy orders
	err := t.client.NewCancelAllOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		logger.Infof("  ⚠ Failed to cancel legacy orders: %v", err)
	} else {
		logger.Infof("  ✓ Canceled all legacy pending orders for %s", symbol)
	}

	// 2. Cancel all Algo orders
	err = t.client.NewCancelAllAlgoOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		// Ignore "no algo orders" error
		if !contains(err.Error(), "no algo") && !contains(err.Error(), "No algo") {
			logger.Infof("  ⚠ Failed to cancel Algo orders: %v", err)
		}
	} else {
		logger.Infof("  ✓ Canceled all Algo orders for %s", symbol)
	}

	return nil
}

// PlaceLimitOrder places a limit order for grid trading
// This implements the GridTrader interface for FuturesTrader
func (t *FuturesTrader) PlaceLimitOrder(req *types.LimitOrderRequest) (*types.LimitOrderResult, error) {
	// Format quantity to correct precision
	quantityStr, err := t.FormatQuantity(req.Symbol, req.Quantity)
	if err != nil {
		return nil, fmt.Errorf("failed to format quantity: %w", err)
	}

	// Format price to correct precision
	priceStr, err := t.FormatPrice(req.Symbol, req.Price)
	if err != nil {
		return nil, fmt.Errorf("failed to format price: %w", err)
	}

	// Set leverage if specified
	if req.Leverage > 0 {
		if err := t.SetLeverage(req.Symbol, req.Leverage); err != nil {
			logger.Warnf("Failed to set leverage: %v", err)
		}
	}

	// Determine side and position side
	var side futures.SideType
	var positionSide futures.PositionSideType

	if strings.EqualFold(strings.TrimSpace(req.Side), "BUY") {
		side = futures.SideTypeBuy
	} else {
		side = futures.SideTypeSell
	}
	if normalized := strings.ToUpper(strings.TrimSpace(req.PositionSide)); normalized == "LONG" || normalized == "SHORT" {
		positionSide = futures.PositionSideType(normalized)
	} else if req.Side == "BUY" {
		positionSide = futures.PositionSideTypeLong
	} else {
		positionSide = futures.PositionSideTypeShort
	}

	// Build order service with broker ID
	orderService := t.client.NewCreateOrderService().
		Symbol(req.Symbol).
		Side(side).
		PositionSide(positionSide).
		Type(futures.OrderTypeLimit).
		ReduceOnly(req.ReduceOnly).
		TimeInForce(futures.TimeInForceTypeGTC).
		Quantity(quantityStr).
		Price(priceStr).
		NewClientOrderID(getBrOrderID())

	// Execute order
	order, err := orderService.Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to place limit order: %w", err)
	}

	logger.Infof("✓ [Grid] Placed limit order: %s %s %s @ %s, qty=%s, orderID=%d",
		req.Symbol, req.Side, positionSide, priceStr, quantityStr, order.OrderID)

	return &types.LimitOrderResult{
		OrderID:      fmt.Sprintf("%d", order.OrderID),
		ClientID:     order.ClientOrderID,
		Symbol:       order.Symbol,
		Side:         string(order.Side),
		PositionSide: string(order.PositionSide),
		Price:        req.Price,
		Quantity:     req.Quantity,
		Status:       string(order.Status),
	}, nil
}

func (t *FuturesTrader) submitReduceOnlyMarketOrder(symbol string, side futures.SideType, positionSide futures.PositionSideType, quantity float64) (map[string]interface{}, error) {
	if quantity <= 0 {
		return nil, fmt.Errorf("reduce quantity must be greater than 0")
	}

	quantityStr, err := t.FormatQuantity(symbol, quantity)
	if err != nil {
		return nil, err
	}

	quantityFloat, parseErr := strconv.ParseFloat(quantityStr, 64)
	if parseErr != nil || quantityFloat <= 0 {
		return nil, fmt.Errorf("reduce quantity too small, rounded to 0 (original: %.8f → formatted: %s)", quantity, quantityStr)
	}

	order, err := t.client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(positionSide).
		Type(futures.OrderTypeMarket).
		ReduceOnly(true).
		Quantity(quantityStr).
		NewClientOrderID(getBrOrderID()).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to submit reduce-only market order: %w", err)
	}

	logger.Infof("✓ Submitted reduce-only market order: %s %s %s quantity=%s", symbol, side, positionSide, quantityStr)

	result := map[string]interface{}{
		"orderId": order.OrderID,
		"symbol":  order.Symbol,
		"status":  order.Status,
	}
	return result, nil
}

// CancelOrder cancels a specific order by ID
// This implements the GridTrader interface for FuturesTrader
func (t *FuturesTrader) CancelOrder(symbol, orderID string) error {
	// Parse order ID to int64
	orderIDInt, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid order ID: %w", err)
	}

	_, err = t.client.NewCancelOrderService().
		Symbol(symbol).
		OrderID(orderIDInt).
		Do(context.Background())

	if err != nil {
		return fmt.Errorf("failed to cancel order: %w", err)
	}

	logger.Infof("✓ [Grid] Cancelled order: %s/%s", symbol, orderID)
	return nil
}

// GetOrderBook gets the order book for a symbol
// This implements the GridTrader interface for FuturesTrader
func (t *FuturesTrader) GetOrderBook(symbol string, depth int) (bids, asks [][]float64, err error) {
	book, err := t.client.NewDepthService().
		Symbol(symbol).
		Limit(depth).
		Do(context.Background())

	if err != nil {
		return nil, nil, fmt.Errorf("failed to get order book: %w", err)
	}

	// Convert bids
	bids = make([][]float64, len(book.Bids))
	for i, bid := range book.Bids {
		price, _ := strconv.ParseFloat(bid.Price, 64)
		qty, _ := strconv.ParseFloat(bid.Quantity, 64)
		bids[i] = []float64{price, qty}
	}

	// Convert asks
	asks = make([][]float64, len(book.Asks))
	for i, ask := range book.Asks {
		price, _ := strconv.ParseFloat(ask.Price, 64)
		qty, _ := strconv.ParseFloat(ask.Quantity, 64)
		asks[i] = []float64{price, qty}
	}

	return bids, asks, nil
}

// CancelStopOrders cancels take-profit/stop-loss orders for this symbol (used to adjust TP/SL positions)
// Now uses both legacy API and new Algo Order API (Binance migrated stop orders to Algo system)
func (t *FuturesTrader) CancelStopOrders(symbol string) error {
	canceledCount := 0

	// 1. Cancel legacy stop orders (for backward compatibility)
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, order := range orders {
			orderType := string(order.Type)

			// Only cancel stop-loss and take-profit orders
			// Use string comparison since OrderType constants were removed in v2.8.9
			if orderType == "STOP_MARKET" ||
				orderType == "TAKE_PROFIT_MARKET" ||
				orderType == "STOP" ||
				orderType == "TAKE_PROFIT" {

				_, err := t.client.NewCancelOrderService().
					Symbol(symbol).
					OrderID(order.OrderID).
					Do(context.Background())

				if err != nil {
					logger.Infof("  ⚠ Failed to cancel legacy order %d: %v", order.OrderID, err)
					continue
				}

				canceledCount++
				logger.Infof("  ✓ Canceled legacy stop order for %s (Order ID: %d, Type: %s)",
					symbol, order.OrderID, orderType)
			}
		}
	}

	// 2. Cancel Algo orders (new API)
	err = t.client.NewCancelAllAlgoOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		// Ignore "no algo orders" error
		if !contains(err.Error(), "no algo") && !contains(err.Error(), "No algo") {
			logger.Infof("  ⚠ Failed to cancel Algo orders: %v", err)
		}
	} else {
		logger.Infof("  ✓ Canceled all Algo orders for %s", symbol)
		canceledCount++
	}

	if canceledCount == 0 {
		logger.Infof("  ℹ %s has no take-profit/stop-loss orders to cancel", symbol)
	}

	return nil
}

// GetOpenOrders gets all open/pending orders for a symbol
func (t *FuturesTrader) GetOpenOrders(symbol string) ([]types.OpenOrder, error) {
	var result []types.OpenOrder

	// 1. Get legacy open orders
	orders, err := t.client.NewListOpenOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err != nil {
		return nil, fmt.Errorf("failed to get open orders: %w", err)
	}

	for _, order := range orders {
		price, _ := strconv.ParseFloat(order.Price, 64)
		stopPrice, _ := strconv.ParseFloat(order.StopPrice, 64)
		quantity, _ := strconv.ParseFloat(order.OrigQuantity, 64)

		result = append(result, types.OpenOrder{
			OrderID:      fmt.Sprintf("%d", order.OrderID),
			Symbol:       order.Symbol,
			Side:         string(order.Side),
			PositionSide: string(order.PositionSide),
			Type:         string(order.Type),
			Price:        price,
			StopPrice:    stopPrice,
			Quantity:     quantity,
			Status:       string(order.Status),
		})
	}

	// 2. Get Algo orders (new API for stop-loss/take-profit)
	algoOrders, err := t.client.NewListOpenAlgoOrdersService().
		Symbol(symbol).
		Do(context.Background())

	if err == nil {
		for _, algoOrder := range algoOrders {
			triggerPrice, _ := strconv.ParseFloat(algoOrder.TriggerPrice, 64)
			quantity, _ := strconv.ParseFloat(algoOrder.Quantity, 64)

			result = append(result, types.OpenOrder{
				OrderID:      fmt.Sprintf("%d", algoOrder.AlgoId),
				Symbol:       algoOrder.Symbol,
				Side:         string(algoOrder.Side),
				PositionSide: string(algoOrder.PositionSide),
				Type:         string(algoOrder.OrderType),
				Price:        0, // Algo orders use stop price
				StopPrice:    triggerPrice,
				Quantity:     quantity,
				Status:       "NEW",
			})
		}
	}

	return result, nil
}

// SetStopLoss sets stop-loss order using new Algo Order API
// Binance has migrated stop orders to Algo Order system (error -4120 STOP_ORDER_SWITCH_ALGO)
func (t *FuturesTrader) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	_, err := t.CreateStopLossOrder(symbol, positionSide, quantity, stopPrice, "")
	return err
}

// CreateStopLossOrder submits a stop-loss algo order and returns the client algo id used for correlation.
// The client algo id is the system-local correlation key for the protection truth layer.
func (t *FuturesTrader) CreateStopLossOrder(symbol string, positionSide string, quantity, stopPrice float64, clientAlgoID string) (string, error) {
	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	if clientAlgoID == "" {
		clientAlgoID = getBrOrderID()
	}

	// Use new Algo Order API
	_, err := t.client.NewCreateAlgoOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.AlgoOrderTypeStopMarket).
		TriggerPrice(fmt.Sprintf("%.8f", stopPrice)).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true).
		ClientAlgoId(clientAlgoID).
		Do(context.Background())

	if err != nil {
		return "", fmt.Errorf("failed to set stop-loss: %w", err)
	}

	logger.Infof("  Stop-loss price set (Algo Order): %.4f", stopPrice)
	return clientAlgoID, nil
}

// SetTakeProfit sets take-profit order using new Algo Order API
// Binance has migrated stop orders to Algo Order system (error -4120 STOP_ORDER_SWITCH_ALGO)
func (t *FuturesTrader) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	_, err := t.CreateTakeProfitOrder(symbol, positionSide, quantity, takeProfitPrice, "")
	return err
}

// CreateTakeProfitOrder submits a take-profit algo order and returns the client algo id used for correlation.
// The client algo id is the system-local correlation key for the protection truth layer.
func (t *FuturesTrader) CreateTakeProfitOrder(symbol string, positionSide string, quantity, takeProfitPrice float64, clientAlgoID string) (string, error) {
	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	if clientAlgoID == "" {
		clientAlgoID = getBrOrderID()
	}

	// Use new Algo Order API
	_, err := t.client.NewCreateAlgoOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.AlgoOrderTypeTakeProfitMarket).
		TriggerPrice(fmt.Sprintf("%.8f", takeProfitPrice)).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true).
		ClientAlgoId(clientAlgoID).
		Do(context.Background())

	if err != nil {
		return "", fmt.Errorf("failed to set take-profit: %w", err)
	}

	logger.Infof("  Take-profit price set (Algo Order): %.4f", takeProfitPrice)
	return clientAlgoID, nil
}

// GetOrderStatus gets order status
func (t *FuturesTrader) GetOrderStatus(symbol string, orderID string) (map[string]interface{}, error) {
	// Convert orderID to int64
	orderIDInt, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid order ID: %s", orderID)
	}

	order, err := t.client.NewGetOrderService().
		Symbol(symbol).
		OrderID(orderIDInt).
		Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get order status: %w", err)
	}

	// Parse execution price
	avgPrice, _ := strconv.ParseFloat(order.AvgPrice, 64)
	executedQty, _ := strconv.ParseFloat(order.ExecutedQuantity, 64)

	result := map[string]interface{}{
		"orderId":     order.OrderID,
		"symbol":      order.Symbol,
		"status":      string(order.Status),
		"avgPrice":    avgPrice,
		"executedQty": executedQty,
		"side":        string(order.Side),
		"type":        string(order.Type),
		"time":        order.Time,
		"updateTime":  order.UpdateTime,
	}

	// Binance futures commission fee needs to be obtained through GetUserTrades, not retrieved here for now
	// Can be obtained later through WebSocket or separate query
	result["commission"] = 0.0

	return result, nil
}
