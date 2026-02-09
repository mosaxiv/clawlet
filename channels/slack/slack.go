package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mosaxiv/picoclaw/bus"
	"github.com/mosaxiv/picoclaw/channels"
	"github.com/mosaxiv/picoclaw/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type Channel struct {
	cfg   config.SlackConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	api *slack.Client
	hc  *http.Client
}

func New(cfg config.SlackConfig, b *bus.Bus) *Channel {
	hc := &http.Client{Timeout: 20 * time.Second}
	return &Channel{
		cfg:   cfg,
		bus:   b,
		allow: channels.AllowList{AllowFrom: cfg.AllowFrom},
		hc:    hc,
		api:   slack.New(strings.TrimSpace(cfg.BotToken), slack.OptionHTTPClient(hc)),
	}
}

func (c *Channel) Name() string    { return "slack" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	// Inbound is handled by Events API HTTP endpoint; keep this running for status parity.
	c.running.Store(true)
	<-ctx.Done()
	c.running.Store(false)
	return ctx.Err()
}

func (c *Channel) Stop() error { c.running.Store(false); return nil }

// EventsHandler returns an http.HandlerFunc for Slack Events API endpoint.
func (c *Channel) EventsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if strings.TrimSpace(c.cfg.SigningSecret) == "" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("slack signingSecret not configured"))
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		_ = r.Body.Close()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		verifier, err := slack.NewSecretsVerifier(r.Header, c.cfg.SigningSecret)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = verifier.Write(body)
		if err := verifier.Ensure(); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		ev, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if ev.Type == slackevents.URLVerification {
			data, ok := ev.Data.(*slackevents.EventsAPIURLVerificationEvent)
			if !ok || data == nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(data.Challenge))
			return
		}

		// Always ack quickly.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))

		// Process in background.
		go c.handleEvent(context.Background(), ev)
	}
}

func (c *Channel) handleEvent(ctx context.Context, ev slackevents.EventsAPIEvent) {
	if ev.Type != slackevents.CallbackEvent {
		return
	}
	if ev.InnerEvent.Type != "message" {
		return
	}
	mev, ok := ev.InnerEvent.Data.(*slackevents.MessageEvent)
	if !ok || mev == nil {
		return
	}
	// Ignore bot messages / message_changed etc.
	if strings.TrimSpace(mev.BotID) != "" || strings.TrimSpace(mev.SubType) != "" {
		return
	}
	user := strings.TrimSpace(mev.User)
	ch := strings.TrimSpace(mev.Channel)
	text := strings.TrimSpace(mev.Text)
	if user == "" || ch == "" || text == "" {
		return
	}
	if !c.allow.Allowed(user) {
		return
	}
	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:    "slack",
		SenderID:   user,
		ChatID:     ch,
		Content:    text,
		SessionKey: "slack:" + ch,
	})
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.BotToken) == "" {
		return fmt.Errorf("slack botToken is empty")
	}
	ch := strings.TrimSpace(msg.ChatID)
	if ch == "" {
		return fmt.Errorf("chat_id is empty")
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return nil
	}
	if c.api == nil {
		c.api = slack.New(strings.TrimSpace(c.cfg.BotToken), slack.OptionHTTPClient(c.hc))
	}
	_, _, err := c.api.PostMessageContext(ctx, ch, slack.MsgOptionText(text, false))
	return err
}
