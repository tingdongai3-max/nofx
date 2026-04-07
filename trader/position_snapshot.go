package trader

import (
	"fmt"
	"math"
	"nofx/logger"
	"nofx/market"
	"nofx/store"
	"strconv"
	"strings"
	"time"
)

// CreatePositionSnapshot gets current real positions from exchange and creates snapshot positions
// This function will:
// 1. Delete all OPEN old positions from database
// 2. Get current real positions from exchange
// 3. Create a "snapshot" record for each real position
func CreatePositionSnapshot(traderID, exchangeID, exchangeType string, trader Trader, st *store.Store) error {
	logger.Infof("📸 Creating position snapshot for trader %s (%s)...", traderID, exchangeType)

	positionStore := st.Position()

	// Step 1: Delete all OPEN positions
	logger.Infof("🗑️  Deleting all OPEN positions from database...")
	if err := positionStore.DeleteAllOpenPositions(traderID); err != nil {
		return fmt.Errorf("failed to delete open positions: %w", err)
	}
	logger.Infof("✅ Deleted all OPEN positions")

	// Step 2: Get current positions from exchange
	logger.Infof("📡 Fetching current positions from exchange...")
	positions, err := trader.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions from exchange: %w", err)
	}

	if len(positions) == 0 {
		logger.Infof("✅ No open positions on exchange, snapshot complete")
		return nil
	}

	logger.Infof("📥 Found %d positions on exchange", len(positions))

	// Step 3: Create snapshot record for each position
	nowMs := time.Now().UnixMilli()
	createdCount := 0

	for _, posMap := range positions {
		// Parse position data
		rawSymbol, _ := posMap["symbol"].(string)
		symbol := market.Normalize(rawSymbol)
		sideStr, _ := posMap["side"].(string)
		positionAmt, _ := posMap["positionAmt"].(float64)
		entryPrice, _ := posMap["entryPrice"].(float64)
		markPrice, _ := posMap["markPrice"].(float64)
		leverage, _ := posMap["leverage"].(float64)

		// Skip positions with 0 quantity
		if positionAmt == 0 {
			continue
		}

		// Determine position side
		side := "LONG"
		if sideStr == "short" {
			side = "SHORT"
		}

		// Use current mark price as entry price (approximation)
		// If entryPrice is 0, use markPrice
		if entryPrice == 0 {
			entryPrice = markPrice
		}

		snapshotPosition := &store.TraderPosition{
			TraderID:           traderID,
			ExchangeID:         exchangeID,
			ExchangeType:       exchangeType,
			ExchangePositionID: fmt.Sprintf("snapshot_%s_%s_%d", symbol, side, nowMs),
			Symbol:             symbol,
			Side:               side,
			Quantity:           positionAmt,
			EntryPrice:         entryPrice,
			EntryOrderID:       "snapshot", // Mark as snapshot
			EntryTime:          nowMs,
			Leverage:           int(leverage),
			Status:             "OPEN",
			Source:             "snapshot", // Mark source as snapshot
			CreatedAt:          nowMs,
			UpdatedAt:          nowMs,
		}

		if err := positionStore.CreateOpenPosition(snapshotPosition); err != nil {
			logger.Infof("  ⚠️ Failed to create snapshot position for %s %s: %v", symbol, side, err)
			continue
		}

		logger.Infof("  ✅ Created snapshot: %s %s %.6f @ %.2f (leverage: %dx)",
			symbol, side, positionAmt, entryPrice, int(leverage))
		createdCount++
	}

	logger.Infof("✅ Position snapshot complete: %d positions created", createdCount)
	return nil
}

// CollectLivePositionSnapshots reads current exchange positions and converts them into system snapshot rows.
// It is shared by runtime previews and recovery coordinators so position parsing happens in one place.
func CollectLivePositionSnapshots(trader Trader) ([]store.PositionSnapshot, error) {
	if trader == nil {
		return nil, nil
	}

	positions, err := trader.GetPositions()
	if err != nil {
		return nil, err
	}

	snapshots := make([]store.PositionSnapshot, 0, len(positions))
	for _, pos := range positions {
		symbol, _ := pos["symbol"].(string)
		side, _ := pos["side"].(string)
		entryPrice := safePositionFloat(pos, "entry_price", "entryPrice")
		markPrice := safePositionFloat(pos, "mark_price", "markPrice")
		quantity := math.Abs(safePositionFloat(pos, "quantity", "positionAmt"))
		unrealizedPnL := safePositionFloat(pos, "unrealized_pnl", "unRealizedProfit")
		leverage := safePositionFloat(pos, "leverage")
		liquidationPrice := safePositionFloat(pos, "liquidation_price", "liquidationPrice")

		if symbol == "" || side == "" {
			continue
		}

		snapshots = append(snapshots, store.PositionSnapshot{
			Symbol:           market.Normalize(symbol),
			Side:             strings.ToUpper(strings.TrimSpace(side)),
			PositionAmt:      quantity,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			UnrealizedProfit: unrealizedPnL,
			Leverage:         leverage,
			LiquidationPrice: liquidationPrice,
		})
	}

	return snapshots, nil
}

func safePositionFloat(data map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			switch v := value.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case uint64:
				return float64(v)
			case string:
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}
