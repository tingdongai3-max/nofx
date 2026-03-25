package store

import "testing"

func TestAdaptiveMemoryConfigRoundTrip(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	st, err := NewFromGorm(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	cfg, err := st.GetAdaptiveMemoryConfig()
	if err != nil {
		t.Fatalf("get default adaptive config: %v", err)
	}
	wantDefault := DefaultAdaptiveMemoryConfig()
	if cfg != wantDefault {
		t.Fatalf("expected default adaptive memory config %+v, got %+v", wantDefault, cfg)
	}

	updated := AdaptiveMemorySystemConfig{
		GlobalSamples: 500,
		SectorSamples: 300,
		SymbolSamples: 100,
	}
	if err := st.SetAdaptiveMemoryConfig(updated); err != nil {
		t.Fatalf("set adaptive config: %v", err)
	}

	got, err := st.GetAdaptiveMemoryConfig()
	if err != nil {
		t.Fatalf("get updated adaptive config: %v", err)
	}
	if got != updated {
		t.Fatalf("expected updated adaptive memory config %+v, got %+v", updated, got)
	}
}

func TestResonanceGuardConfigRoundTrip(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	st, err := NewFromGorm(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	cfg, err := st.GetResonanceGuardConfig()
	if err != nil {
		t.Fatalf("get default resonance config: %v", err)
	}
	wantDefault := DefaultResonanceGuardConfig()
	if cfg != wantDefault {
		t.Fatalf("expected default resonance guard config %+v, got %+v", wantDefault, cfg)
	}

	updated := ResonanceGuardSystemConfig{
		AdaptiveEntryFloor:  45,
		AdaptiveEntryLambda: 0.15,
	}
	if err := st.SetResonanceGuardConfig(updated); err != nil {
		t.Fatalf("set resonance config: %v", err)
	}

	got, err := st.GetResonanceGuardConfig()
	if err != nil {
		t.Fatalf("get updated resonance config: %v", err)
	}
	if got != updated {
		t.Fatalf("expected updated resonance guard config %+v, got %+v", updated, got)
	}
}
