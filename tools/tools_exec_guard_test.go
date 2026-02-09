package tools

import (
	"path/filepath"
	"testing"
)

func TestGuardExecCommand_DenyPatterns(t *testing.T) {
	ws := filepath.Clean("/tmp/ws")
	cases := []string{
		"rm -rf /",
		"rm -r ./foo",
		"shutdown now",
		"dd if=/dev/zero of=/dev/null",
	}
	for _, c := range cases {
		if msg := guardExecCommand(c, ws, true); msg == "" {
			t.Fatalf("expected blocked for %q", c)
		}
	}
}

func TestGuardExecCommand_PathOutsideWorkspaceWhenRestricted(t *testing.T) {
	ws := filepath.Clean("/tmp/ws")

	if msg := guardExecCommand("cat /etc/hosts", ws, true); msg == "" {
		t.Fatalf("expected blocked for absolute path outside workspace")
	}
	if msg := guardExecCommand("cat ../secrets.txt", ws, true); msg == "" {
		t.Fatalf("expected blocked for path traversal")
	}
	if msg := guardExecCommand("cat ~/notes.txt", ws, true); msg == "" {
		t.Fatalf("expected blocked for home expansion")
	}
}

func TestGuardExecCommand_AllowsRelativePathsInWorkspaceWhenRestricted(t *testing.T) {
	ws := filepath.Clean("/tmp/ws")
	if msg := guardExecCommand("cat ./hello.txt", ws, true); msg != "" {
		t.Fatalf("expected allowed, got: %q", msg)
	}
	if msg := guardExecCommand("go test ./...", ws, true); msg != "" {
		t.Fatalf("expected allowed, got: %q", msg)
	}
}

func TestGuardExecCommand_AllowsAbsoluteWhenNotRestricted(t *testing.T) {
	ws := filepath.Clean("/tmp/ws")
	if msg := guardExecCommand("cat /etc/hosts", ws, false); msg != "" {
		t.Fatalf("expected allowed when not restricted, got: %q", msg)
	}
}
