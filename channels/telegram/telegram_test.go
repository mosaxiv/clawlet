package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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
		got := telegramSenderID(&models.User{ID: 1001, Username: "@alice"})
		if got != "1001|alice" {
			t.Fatalf("unexpected sender id: %q", got)
		}
	})

	t.Run("id only", func(t *testing.T) {
		got := telegramSenderID(&models.User{ID: 1002})
		if got != "1002" {
			t.Fatalf("unexpected sender id: %q", got)
		}
	})
}

func TestBuildTelegramDelivery(t *testing.T) {
	msg := &models.Message{
		ID:              77,
		MessageThreadID: 123456,
		Chat: models.Chat{
			ID:   9,
			Type: models.ChatTypePrivate,
		},
		ReplyToMessage: &models.Message{
			ID: 66,
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

func TestClampTelegramWorkers(t *testing.T) {
	if got := clampTelegramWorkers(0); got != 2 {
		t.Fatalf("expected default 2, got %d", got)
	}
	if got := clampTelegramWorkers(99); got != 8 {
		t.Fatalf("expected max 8, got %d", got)
	}
	if got := clampTelegramWorkers(4); got != 4 {
		t.Fatalf("expected 4, got %d", got)
	}
}

func TestShouldRetryTelegramSend(t *testing.T) {
	t.Run("retry on too many requests", func(t *testing.T) {
		retry, wait := shouldRetryTelegramSend(&tgbot.TooManyRequestsError{RetryAfter: 2}, 1)
		if !retry || wait != 2*time.Second {
			t.Fatalf("expected retry wait=2s, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("retry on 5xx", func(t *testing.T) {
		err := errors.New("error response from telegram for method sendMessage, 503 service unavailable")
		retry, wait := shouldRetryTelegramSend(err, 2)
		if !retry || wait <= 0 {
			t.Fatalf("expected retry, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("no retry on context cancel", func(t *testing.T) {
		retry, wait := shouldRetryTelegramSend(context.Canceled, 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry, got retry=%v wait=%v", retry, wait)
		}
	})
}

func TestMarkdownToTelegramHTML(t *testing.T) {
	in := "# Title\n**bold** _italic_ ~~strike~~\n- item\n`x<y`"
	got := markdownToTelegramHTML(in)

	checks := []string{
		"Title",
		"<b>bold</b>",
		"<i>italic</i>",
		"<s>strike</s>",
		"• item",
		"<code>x&lt;y</code>",
	}
	for _, s := range checks {
		if !strings.Contains(got, s) {
			t.Fatalf("expected %q in %q", s, got)
		}
	}
}

func TestIsTelegramParseError(t *testing.T) {
	err := errors.New("error response from telegram for method sendMessage, 400 Bad Request: can't parse entities")
	if !isTelegramParseError(err) {
		t.Fatalf("expected parse error detection")
	}
	if isTelegramParseError(errors.New("error response from telegram for method sendMessage, 403 Forbidden")) {
		t.Fatalf("unexpected parse error detection")
	}
}
