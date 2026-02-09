package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRegistryDefinitions_GatedByCapabilities(t *testing.T) {
	r := &Registry{
		WorkspaceDir:        "/tmp",
		RestrictToWorkspace: false,
		ExecTimeout:         1 * time.Second,
		BraveAPIKey:         "",
		Outbound:            nil,
		Spawn:               nil,
		Cron:                nil,
		ReadSkill:           nil,
	}

	defs := r.Definitions()
	has := map[string]bool{}
	for _, d := range defs {
		if n := d.Function.Name; n != "" {
			has[n] = true
		}
	}

	// Always present.
	for _, n := range []string{"read_file", "write_file", "edit_file", "list_dir", "exec", "web_fetch"} {
		if !has[n] {
			t.Fatalf("expected tool definition: %s", n)
		}
	}

	// Capability-gated.
	for _, n := range []string{"web_search", "message", "spawn", "cron", "read_skill"} {
		if has[n] {
			t.Fatalf("did not expect tool definition: %s", n)
		}
	}

	// Execute unknown tool should still error.
	if _, err := r.Execute(context.Background(), Context{Channel: "cli", ChatID: "direct"}, "message", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("expected error executing disabled tool")
	}
}
