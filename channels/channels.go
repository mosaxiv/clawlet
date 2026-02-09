package channels

import (
	"context"
	"strings"

	"github.com/mosaxiv/picoclaw/bus"
)

type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop() error
	Send(ctx context.Context, msg bus.OutboundMessage) error
	IsRunning() bool
}

type AllowList struct {
	AllowFrom []string
}

func (a AllowList) Allowed(senderID string) bool {
	if len(a.AllowFrom) == 0 {
		return true
	}
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return false
	}
	for _, v := range a.AllowFrom {
		if senderID == v {
			return true
		}
	}
	// Accept compound IDs (e.g. "a|b")
	if strings.Contains(senderID, "|") {
		parts := strings.Split(senderID, "|")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			for _, v := range a.AllowFrom {
				if p == v {
					return true
				}
			}
		}
	}
	return false
}
