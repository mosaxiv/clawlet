package agent

import (
	"testing"

	"github.com/mosaxiv/clawlet/session"
)

func TestNormalizeSlashCommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "help", in: "/help", want: "/help"},
		{name: "with args", in: "/help please", want: "/help"},
		{name: "bot suffix", in: "/new@my_bot", want: "/new"},
		{name: "not command", in: "hello", want: "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeSlashCommand(tt.in); got != tt.want {
				t.Fatalf("normalizeSlashCommand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHandleSlashCommand(t *testing.T) {
	t.Run("new clears history", func(t *testing.T) {
		sess := session.New("telegram:1")
		sess.Add("user", "hello")
		sess.Add("assistant", "world")

		handled, out := handleSlashCommand(sess, "/new")
		if !handled {
			t.Fatalf("expected handled")
		}
		if out == "" {
			t.Fatalf("expected response")
		}
		if len(sess.Messages) != 0 {
			t.Fatalf("expected cleared messages, got %d", len(sess.Messages))
		}
	})

	t.Run("help handled", func(t *testing.T) {
		handled, out := handleSlashCommand(session.New("telegram:1"), "/help")
		if !handled {
			t.Fatalf("expected handled")
		}
		if out == "" {
			t.Fatalf("expected non-empty response")
		}
	})

	t.Run("unknown not handled", func(t *testing.T) {
		handled, out := handleSlashCommand(session.New("telegram:1"), "/unknown")
		if handled || out != "" {
			t.Fatalf("expected not handled, got handled=%v out=%q", handled, out)
		}
	})
}
