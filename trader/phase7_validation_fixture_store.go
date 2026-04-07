package trader

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"nofx/store"
)

const defaultPhase7ValidationFixtureDBPath = "data/phase7_validation_fixtures.db"

var phase7ValidationFixtureStoreMu sync.Mutex
var phase7ValidationFixtureStoreByPath = make(map[string]*store.Store)

// phase7ValidationFixtureDBPath returns the isolated validation-fixture database path.
// It is validation-only and never points at the live trading database.
func phase7ValidationFixtureDBPath() string {
	if path := os.Getenv("PHASE7_VALIDATION_DB_PATH"); path != "" {
		return filepath.Clean(path)
	}
	return defaultPhase7ValidationFixtureDBPath
}

// openPhase7ValidationFixtureStore opens the isolated validation-fixture store.
// It is validation-only and keeps replay/migration fixtures out of the live database.
func openPhase7ValidationFixtureStore() (*store.Store, error) {
	path := phase7ValidationFixtureDBPath()

	phase7ValidationFixtureStoreMu.Lock()
	defer phase7ValidationFixtureStoreMu.Unlock()

	if existing := phase7ValidationFixtureStoreByPath[path]; existing != nil {
		return existing, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create validation fixture directory: %w", err)
	}

	fixtureStore, err := store.New(path)
	if err != nil {
		return nil, err
	}

	phase7ValidationFixtureStoreByPath[path] = fixtureStore
	return fixtureStore, nil
}
