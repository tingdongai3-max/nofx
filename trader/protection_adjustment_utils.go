package trader

import "strings"

// chooseString returns the first non-empty trimmed string.
// It is an execution-side helper used by protection adjustment flows, not a truth-layer state.
func chooseString(primary, fallback string) string {
	primary = strings.TrimSpace(primary)
	if primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}
