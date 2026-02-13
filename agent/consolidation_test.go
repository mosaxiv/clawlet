package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mosaxiv/clawlet/session"
)

func TestMaybeConsolidateSession_NoOpWhenUnderWindow(t *testing.T) {
	ws := t.TempDir()
	sess := session.New("cli:test")
	for range 6 {
		sess.Add("user", "msg")
		sess.Add("assistant", "reply")
	}

	done, err := maybeConsolidateSession(context.Background(), ws, sess, 20, nil)
	if err != nil {
		t.Fatalf("maybeConsolidateSession error: %v", err)
	}
	if done {
		t.Fatalf("unexpected consolidation")
	}
	if len(sess.Messages) != 12 {
		t.Fatalf("messages=%d", len(sess.Messages))
	}
}

func TestMaybeConsolidateSession_TrimAndArchive(t *testing.T) {
	ws := t.TempDir()
	sess := session.New("cli:test")
	for range 15 {
		sess.Add("user", "question")
		sess.AddWithTools("assistant", "answer", []string{"read_file", "exec"})
	}

	summarize := func(ctx context.Context, currentMemory, conversation string) (string, string, error) {
		if !strings.Contains(conversation, "USER: question") {
			t.Fatalf("unexpected conversation: %s", conversation)
		}
		if !strings.Contains(conversation, "ASSISTANT [tools: read_file, exec]: answer") {
			t.Fatalf("missing tools_used in conversation: %s", conversation)
		}
		return "[2026-02-13 23:20] archived summary", "# Long-term Memory\n\n- prefers concise Japanese\n", nil
	}
	done, err := maybeConsolidateSession(context.Background(), ws, sess, 20, summarize)
	if err != nil {
		t.Fatalf("maybeConsolidateSession error: %v", err)
	}
	if !done {
		t.Fatalf("expected consolidation")
	}

	// keep=min(10, max(2, 20/2)) => 10
	if len(sess.Messages) != 10 {
		t.Fatalf("messages=%d", len(sess.Messages))
	}

	historyPath := filepath.Join(ws, "memory", "HISTORY.md")
	b, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("read HISTORY.md: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "# Session History") {
		t.Fatalf("missing history header")
	}
	if !strings.Contains(content, "archived summary") {
		t.Fatalf("missing history entry: %s", content)
	}

	memPath := filepath.Join(ws, "memory", "MEMORY.md")
	mem, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if !strings.Contains(string(mem), "prefers concise Japanese") {
		t.Fatalf("memory not updated: %s", string(mem))
	}
}

func TestMaybeConsolidateSession_SummarizeError_NoTrim(t *testing.T) {
	ws := t.TempDir()
	sess := session.New("cli:test")
	for range 15 {
		sess.Add("user", "question")
		sess.Add("assistant", "answer")
	}

	summarize := func(ctx context.Context, currentMemory, conversation string) (string, string, error) {
		return "", "", context.DeadlineExceeded
	}
	done, err := maybeConsolidateSession(context.Background(), ws, sess, 20, summarize)
	if err == nil {
		t.Fatalf("expected error")
	}
	if done {
		t.Fatalf("unexpected done=true on error")
	}
	if len(sess.Messages) != 30 {
		t.Fatalf("messages=%d", len(sess.Messages))
	}
}
