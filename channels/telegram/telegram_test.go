package telegram

import (
	"testing"

	"github.com/mosaxiv/clawlet/bus"
)

func TestResolveTelegramReplyTarget(t *testing.T) {
	t.Run("prefer typed delivery reply id", func(t *testing.T) {
		got := resolveTelegramReplyTarget(bus.OutboundMessage{
			ReplyTo: "12",
			Delivery: bus.Delivery{
				ReplyToID: "34",
			},
		})
		if got != 34 {
			t.Fatalf("expected 34, got %d", got)
		}
	})

	t.Run("fallback legacy reply_to", func(t *testing.T) {
		got := resolveTelegramReplyTarget(bus.OutboundMessage{
			ReplyTo: "56",
		})
		if got != 56 {
			t.Fatalf("expected 56, got %d", got)
		}
	})

	t.Run("invalid values", func(t *testing.T) {
		got := resolveTelegramReplyTarget(bus.OutboundMessage{
			ReplyTo: "abc",
			Delivery: bus.Delivery{
				ReplyToID: "def",
			},
		})
		if got != 0 {
			t.Fatalf("expected 0, got %d", got)
		}
	})
}

func TestTelegramSenderID(t *testing.T) {
	t.Run("id and username", func(t *testing.T) {
		got := telegramSenderID(&telegramUser{ID: 1001, Username: "@alice"})
		if got != "1001|alice" {
			t.Fatalf("unexpected sender id: %q", got)
		}
	})

	t.Run("id only", func(t *testing.T) {
		got := telegramSenderID(&telegramUser{ID: 1002})
		if got != "1002" {
			t.Fatalf("unexpected sender id: %q", got)
		}
	})
}

func TestBuildTelegramDelivery(t *testing.T) {
	msg := &telegramMessage{
		MessageID:       77,
		MessageThreadID: 123456,
		Chat: telegramChat{
			ID:   9,
			Type: "private",
		},
		ReplyToMessage: &telegramMessage{
			MessageID: 66,
		},
	}

	d := buildTelegramDelivery(msg)
	if d.MessageID != "77" || d.ReplyToID != "66" || d.ThreadID != "123456" || !d.IsDirect {
		t.Fatalf("unexpected delivery: %+v", d)
	}
}

func TestClampTelegramPollTimeout(t *testing.T) {
	if got := clampTelegramPollTimeout(0); got != 25 {
		t.Fatalf("expected default 25, got %d", got)
	}
	if got := clampTelegramPollTimeout(90); got != 50 {
		t.Fatalf("expected max 50, got %d", got)
	}
	if got := clampTelegramPollTimeout(15); got != 15 {
		t.Fatalf("expected 15, got %d", got)
	}
}
