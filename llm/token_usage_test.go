package llm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTokenUsageStore_AddAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	store, err := NewTokenUsageStore(path)
	if err != nil {
		t.Fatalf("NewTokenUsageStore: %v", err)
	}

	// Add usage for today.
	if err := store.Add(10, 20, 30); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Ensure file was written.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(b) == 0 {
		t.Fatalf("expected non-empty file")
	}

	// Reload and check values.
	store2, err := NewTokenUsageStore(path)
	if err != nil {
		t.Fatalf("NewTokenUsageStore (reload): %v", err)
	}
	day := time.Now().UTC().Format("2006-01-02")
	usage, ok := store2.Get(day)
	if !ok {
		t.Fatalf("expected usage for %q", day)
	}
	if usage.Calls != 1 || usage.PromptTokens != 10 || usage.CompletionTokens != 20 || usage.TotalTokens != 30 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}
