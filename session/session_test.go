package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoad_RewriteSnapshot(t *testing.T) {
	dir := t.TempDir()
	key := "cli:test"
	s := New(key)
	s.Add("user", "u1")
	s.AddWithTools("assistant", "a1", []string{"read_file"})
	if err := Save(dir, s); err != nil {
		t.Fatalf("save #1: %v", err)
	}

	s.Add("user", "u2")
	s.AddWithTools("assistant", "a2", []string{"exec"})
	if err := Save(dir, s); err != nil {
		t.Fatalf("save #2: %v", err)
	}

	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(key, ":", "_"))+".jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	metaLines := 0
	for _, line := range lines {
		if strings.Contains(line, `"_type":"metadata"`) {
			metaLines++
		}
	}
	if metaLines != 1 {
		t.Fatalf("metadata lines=%d", metaLines)
	}

	loaded, err := Load(dir, key)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatalf("loaded session is nil")
	}
	if got := len(loaded.Messages); got != 4 {
		t.Fatalf("messages=%d", got)
	}
	if got := strings.Join(loaded.Messages[1].ToolsUsed, ","); got != "read_file" {
		t.Fatalf("tools_used[1]=%q", got)
	}
	if got := strings.Join(loaded.Messages[3].ToolsUsed, ","); got != "exec" {
		t.Fatalf("tools_used[3]=%q", got)
	}
}

func TestSave_AfterConsolidationPersistsTrimmedMessages(t *testing.T) {
	dir := t.TempDir()
	key := "cli:test"
	s := New(key)
	for range 8 {
		s.Add("user", "q")
	}
	if err := Save(dir, s); err != nil {
		t.Fatalf("save #1: %v", err)
	}

	_, keep, ver, ok := s.SnapshotForConsolidation(4)
	if !ok {
		t.Fatalf("expected snapshot")
	}
	if !s.ApplyConsolidation(ver, keep) {
		t.Fatalf("apply consolidation failed")
	}
	if err := Save(dir, s); err != nil {
		t.Fatalf("save #2: %v", err)
	}

	loaded, err := Load(dir, key)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatalf("loaded session is nil")
	}
	if got := len(loaded.Messages); got != keep {
		t.Fatalf("messages=%d want=%d", got, keep)
	}
}
