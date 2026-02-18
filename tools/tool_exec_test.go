package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExec_DoesNotLeakNonSafeEnvironmentVariables(t *testing.T) {
	t.Setenv("CLAWLET_EXEC_TEST_SECRET", "super-secret")

	r := &Registry{
		WorkspaceDir:        t.TempDir(),
		RestrictToWorkspace: true,
		ExecTimeout:         5 * time.Second,
	}

	out, err := r.exec(context.Background(), "env")
	if err != nil {
		t.Fatalf("exec returned error: %v", err)
	}
	if strings.Contains(out, "CLAWLET_EXEC_TEST_SECRET=super-secret") {
		t.Fatalf("secret env var leaked to exec output")
	}
}

func TestExec_PreservesSafeEnvironmentVariables(t *testing.T) {
	r := &Registry{
		WorkspaceDir:        t.TempDir(),
		RestrictToWorkspace: true,
		ExecTimeout:         5 * time.Second,
	}

	out, err := r.exec(context.Background(), "echo \"$PATH\"")
	if err != nil {
		t.Fatalf("exec returned error: %v", err)
	}
	if !strings.Contains(out, "stdout:\n") {
		t.Fatalf("expected stdout in result, got: %q", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		t.Fatalf("expected non-empty PATH in output, got: %q", out)
	}
}
