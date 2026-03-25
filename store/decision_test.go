package store

import (
	"testing"
	"time"
)

func TestDecisionStoreLogDecisionPersistsAIResponseMeta(t *testing.T) {
	db := openTelemetryTestDB(t, "file::memory:?cache=shared")
	ds := NewDecisionStore(db)
	if err := ds.initTables(); err != nil {
		t.Fatalf("init tables: %v", err)
	}

	record := &DecisionRecord{
		TraderID:            "trader-meta",
		CycleNumber:         42,
		Timestamp:           time.Date(2026, 3, 24, 7, 51, 0, 0, time.UTC),
		RawResponse:         "<reasoning>partial</reasoning>",
		AIFinishReason:      "length",
		AIPromptTokens:      19759,
		AICompletionTokens:  1996,
		AITotalTokens:       21755,
		AIRawBodyTail:       `{"finish_reason":"length","usage":{"prompt_tokens":19759,"completion_tokens":1996,"total_tokens":21755}}`,
		AIMaxTokens:         4096,
		ExecutionLog:        []string{"AI meta: finish_reason=\"length\" usage(prompt=19759 completion=1996 total=21755) max_tokens=4096"},
		Success:             true,
		AIRequestDurationMs: 15000,
	}

	if err := ds.LogDecision(record); err != nil {
		t.Fatalf("log decision: %v", err)
	}

	records, err := ds.GetLatestRecords("trader-meta", 1)
	if err != nil {
		t.Fatalf("get latest records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	got := records[0]
	if got.AIFinishReason != "length" {
		t.Fatalf("expected finish reason length, got %q", got.AIFinishReason)
	}
	if got.AIPromptTokens != 19759 || got.AICompletionTokens != 1996 || got.AITotalTokens != 21755 {
		t.Fatalf("unexpected usage fields: %+v", got)
	}
	if got.AIMaxTokens != 4096 {
		t.Fatalf("expected max tokens 4096, got %d", got.AIMaxTokens)
	}
	if got.AIRawBodyTail == "" {
		t.Fatal("expected raw body tail to be persisted")
	}
}
