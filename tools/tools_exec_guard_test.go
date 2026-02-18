package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mosaxiv/clawlet/paths"
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

func TestGuardExecCommand_BlocksUnsafeShellConstructs(t *testing.T) {
	ws := filepath.Clean("/tmp/ws")
	cases := []string{
		"echo $(whoami)",
		"echo `whoami`",
		"echo hi > out.txt",
		"echo hi; whoami",
		"echo hi & whoami",
		"echo hi | tee out.txt",
	}
	for _, c := range cases {
		if msg := guardExecCommand(c, ws, true); msg == "" {
			t.Fatalf("expected blocked for %q", c)
		}
	}
}

func TestGuardExecCommand_BlocksSensitiveStatePath(t *testing.T) {
	cfgDir, err := paths.ConfigDir()
	if err != nil || cfgDir == "" {
		t.Skip("config dir unavailable")
	}

	ws := filepath.Clean("/tmp/ws")
	if msg := guardExecCommand("cat "+filepath.Join(cfgDir, "auth", "openai-codex.json"), ws, false); msg == "" {
		t.Fatalf("expected blocked for sensitive state path")
	}
	if msg := guardExecCommand("cat ~/.clawlet/whatsapp-auth/session.db", ws, false); msg == "" {
		home, _ := os.UserHomeDir()
		if home != "" && filepath.Clean(cfgDir) == filepath.Clean(filepath.Join(home, ".clawlet")) {
			t.Fatalf("expected blocked for sensitive home path")
		}
	}
}
