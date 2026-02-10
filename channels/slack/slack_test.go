package slack

import "testing"

func TestStripBotMention(t *testing.T) {
	c := &Channel{botUserID: "U123"}

	tests := []struct {
		in   string
		want string
	}{
		{in: "<@U123> hello", want: "hello"},
		{in: "<@U123>: hello", want: "hello"},
		{in: "<@U123>, hello", want: "hello"},
		{in: "hello <@U123>", want: "hello <@U123>"},
		{in: "<@U999> hello", want: "<@U999> hello"},
	}

	for _, tt := range tests {
		if got := c.stripBotMention(tt.in); got != tt.want {
			t.Fatalf("stripBotMention(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAllowedByPolicy_DMAlwaysAllowed(t *testing.T) {
	c := &Channel{}
	if !c.allowedByPolicy("message", "D123", "im", "hi") {
		t.Fatal("expected dm to be allowed")
	}
	if !c.allowedByPolicy("message", "G123", "mpim", "hi") {
		t.Fatal("expected mpim to be allowed")
	}
}

func TestAllowedByPolicy_GroupOpen(t *testing.T) {
	c := &Channel{}
	c.cfg.GroupPolicy = "open"
	if !c.allowedByPolicy("message", "C123", "channel", "hi") {
		t.Fatal("expected group open to allow message")
	}
	if !c.allowedByPolicy("app_mention", "C123", "channel", "<@U1> hi") {
		t.Fatal("expected group open to allow app_mention")
	}
}

func TestAllowedByPolicy_GroupAllowlist(t *testing.T) {
	c := &Channel{}
	c.cfg.GroupPolicy = "allowlist"
	c.cfg.GroupAllowFrom = []string{"C123"}

	if !c.allowedByPolicy("message", "C123", "channel", "hi") {
		t.Fatal("expected allowlisted channel to be allowed")
	}
	if c.allowedByPolicy("message", "C999", "channel", "hi") {
		t.Fatal("expected non-allowlisted channel to be denied")
	}
}

func TestAllowedByPolicy_GroupMention(t *testing.T) {
	c := &Channel{}
	c.cfg.GroupPolicy = "mention"

	if !c.allowedByPolicy("app_mention", "C123", "channel", "<@U1> hi") {
		t.Fatal("expected app_mention to be allowed in mention policy")
	}
	if c.allowedByPolicy("message", "C123", "channel", "<@U1> hi") {
		t.Fatal("expected message to be denied in mention policy (dedup via app_mention)")
	}
}
