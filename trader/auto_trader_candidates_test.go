package trader

import (
	"nofx/kernel"
	"nofx/market"
	"testing"
	"time"
)

func TestUpdateCandidateSnapshotCarriesSectorFromMarketData(t *testing.T) {
	at := &AutoTrader{
		id:   "trader-1",
		name: "Sector Snapshot Tester",
	}

	ctx := &kernel.Context{
		PriceSnapshotAt: time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC),
		CandidateCoins: []kernel.CandidateCoin{
			{Symbol: "BANANAS31USDT"},
		},
		MarketDataMap: map[string]*market.Data{
			"BANANAS31USDT": {
				Symbol:       "BANANAS31USDT",
				Sector:       "Meme",
				CurrentPrice: 0.012345,
			},
		},
	}

	at.updateCandidateSnapshot(ctx, time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC))

	snapshot := at.GetCandidateSnapshot()
	if len(snapshot.Candidates) != 1 {
		t.Fatalf("expected 1 candidate in snapshot, got %d", len(snapshot.Candidates))
	}
	if snapshot.Candidates[0].Sector != "Meme" {
		t.Fatalf("expected candidate sector to be copied from market data, got %q", snapshot.Candidates[0].Sector)
	}
	if !snapshot.PriceSnapshotAt.Equal(ctx.PriceSnapshotAt) {
		t.Fatalf("expected price snapshot time %s, got %s", ctx.PriceSnapshotAt, snapshot.PriceSnapshotAt)
	}
}

func TestUpdateCandidateSnapshotIgnoresOlderCycle(t *testing.T) {
	at := &AutoTrader{
		id:   "trader-1",
		name: "Out-of-order Snapshot Tester",
		candidateSnapshot: CandidateSnapshot{
			UpdatedAt: time.Date(2026, 3, 23, 16, 6, 0, 0, time.UTC),
			Candidates: []CandidateMarketSnapshot{
				{Symbol: "RIVERUSDT", CurrentPrice: 1.23},
			},
		},
	}

	ctx := &kernel.Context{
		PriceSnapshotAt: time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC),
		CandidateCoins: []kernel.CandidateCoin{
			{Symbol: "BANANAS31USDT"},
		},
		MarketDataMap: map[string]*market.Data{
			"BANANAS31USDT": {
				Symbol:       "BANANAS31USDT",
				Sector:       "Meme",
				CurrentPrice: 0.012345,
			},
		},
	}

	at.updateCandidateSnapshot(ctx, time.Date(2026, 3, 23, 16, 3, 0, 0, time.UTC))

	snapshot := at.GetCandidateSnapshot()
	if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].Symbol != "RIVERUSDT" {
		t.Fatalf("expected newer snapshot to be preserved, got %+v", snapshot.Candidates)
	}
}
