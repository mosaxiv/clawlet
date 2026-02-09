package heartbeat

import "testing"

func TestIsHeartbeatOK(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"HEARTBEAT_OK", true},
		{"heartbeat_ok", true},
		{"Heartbeat OK", true}, // underscores ignored
		{" HEARTBEAT_OK\n", true},
		{"HEARTBEATOK", true},
		{"ok", false},
		{"", false},
		{"nothing to do: HEARTBEAT_OK", true},
	}
	for _, c := range cases {
		if got := isHeartbeatOK(c.in); got != c.want {
			t.Fatalf("isHeartbeatOK(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestIsEmpty(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"\n\n", true},
		{"# Heartbeat Tasks\n\n- [ ]\n", true},
		{"- [ ]\n", true},
		{"- [x]\n", true},
		{"<!-- comment -->\n", true},
		{"- [ ] do something\n", false},
		{"Check something\n", false},
	}
	for _, c := range cases {
		if got := isEmpty(c.in); got != c.want {
			t.Fatalf("isEmpty(%q)=%v want %v", c.in, got, c.want)
		}
	}
}
