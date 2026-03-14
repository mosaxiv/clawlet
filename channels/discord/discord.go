package discord

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
)

type Channel struct {
	cfg   config.DiscordConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	mu  sync.Mutex
	dg  *discordgo.Session
	hc  *http.Client
	ctx context.Context
}

func New(cfg config.DiscordConfig, b *bus.Bus) *Channel {
	return &Channel{
		cfg:   cfg,
		bus:   b,
		allow: channels.AllowList{AllowFrom: cfg.AllowFrom},
		hc: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Channel) Name() string    { return "discord" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("discord token is empty")
	}

	dg, err := discordgo.New("Bot " + strings.TrimSpace(c.cfg.Token))
	if err != nil {
		return err
	}
	// Keep operations bounded; discordgo doesn't take context in most calls.
	dg.Client = c.hc

	if c.cfg.Intents != 0 {
		dg.Identify.Intents = discordgo.Intent(c.cfg.Intents)
	}
	dg.AddHandler(c.onMessageCreate)

	c.mu.Lock()
	c.dg = dg
	c.ctx = ctx
	c.mu.Unlock()

	c.running.Store(true)
	defer c.running.Store(false)
	defer func() {
		_ = dg.Close()
		c.mu.Lock()
		if c.dg == dg {
			c.dg = nil
		}
		c.mu.Unlock()
	}()

	if err := dg.Open(); err != nil {
		return err
	}

	<-ctx.Done()
	return ctx.Err()
}

func (c *Channel) Stop() error {
	c.mu.Lock()
	dg := c.dg
	c.dg = nil
	c.mu.Unlock()
	if dg != nil {
		return dg.Close()
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	chID := strings.TrimSpace(msg.ChatID)
	if chID == "" {
		return fmt.Errorf("chat_id is empty")
	}
	content := strings.TrimSpace(msg.Content)
	attachments := msg.Attachments
	if content == "" && len(attachments) == 0 {
		return nil
	}

	c.mu.Lock()
	dg := c.dg
	c.mu.Unlock()
	if dg == nil {
		return fmt.Errorf("discord not connected")
	}

	// Best-effort cancellation: discordgo doesn't propagate ctx. We at least
	// fail fast if ctx is already cancelled.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	replyToID := resolveDiscordReplyTarget(msg)
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := sendDiscordMessage(dg, chID, content, replyToID, attachments)
		if err == nil {
			return nil
		}
		retry, wait := shouldRetryDiscordSend(err, attempt)
		if !retry || attempt == maxAttempts {
			return err
		}
		log.Printf("discord: send failed (%d/%d), retry in %s: %v", attempt, maxAttempts, wait, err)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (c *Channel) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil {
		return
	}
	if m.Author.Bot {
		return
	}
	if !c.allow.Allowed(m.Author.ID) {
		return
	}
	chID := strings.TrimSpace(m.ChannelID)
	content := strings.TrimSpace(m.Content)
	attachments := discordInboundAttachments(m)
	if chID == "" || (content == "" && len(attachments) == 0) {
		return
	}

	ctx := context.Background()
	c.mu.Lock()
	if c.ctx != nil {
		ctx = c.ctx
	}
	c.mu.Unlock()

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:     "discord",
		SenderID:    m.Author.ID,
		ChatID:      chID,
		Content:     content,
		Attachments: attachments,
		SessionKey:  "discord:" + chID,
		Delivery:    buildDiscordDelivery(m),
	})
}

func discordInboundAttachments(m *discordgo.MessageCreate) []bus.Attachment {
	if m == nil || m.Message == nil || len(m.Attachments) == 0 {
		return nil
	}
	out := make([]bus.Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		if a == nil {
			continue
		}
		url := strings.TrimSpace(a.URL)
		if url == "" {
			continue
		}
		mimeType := strings.TrimSpace(a.ContentType)
		out = append(out, bus.Attachment{
			ID:        strings.TrimSpace(a.ID),
			Name:      strings.TrimSpace(a.Filename),
			MIMEType:  mimeType,
			Kind:      bus.InferAttachmentKind(mimeType),
			SizeBytes: int64(a.Size),
			URL:       url,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func resolveDiscordReplyTarget(msg bus.OutboundMessage) string {
	if replyTo := strings.TrimSpace(msg.Delivery.ReplyToID); replyTo != "" {
		return replyTo
	}
	return strings.TrimSpace(msg.ReplyTo)
}

func buildDiscordDelivery(m *discordgo.MessageCreate) bus.Delivery {
	if m == nil || m.Message == nil {
		return bus.Delivery{}
	}
	d := bus.Delivery{
		MessageID: strings.TrimSpace(m.ID),
		IsDirect:  strings.TrimSpace(m.GuildID) == "",
	}
	if m.MessageReference != nil {
		d.ReplyToID = strings.TrimSpace(m.MessageReference.MessageID)
	}
	if d.ReplyToID == "" && m.ReferencedMessage != nil {
		d.ReplyToID = strings.TrimSpace(m.ReferencedMessage.ID)
	}
	return d
}

func sendDiscordMessage(dg *discordgo.Session, chID, content, replyToID string, attachments []bus.Attachment) error {
	msgSend := &discordgo.MessageSend{
		Content: content,
	}

	// Add reply reference if needed
	if replyToID != "" {
		msgSend.Reference = &discordgo.MessageReference{
			MessageID: replyToID,
			ChannelID: chID,
		}
		msgSend.AllowedMentions = &discordgo.MessageAllowedMentions{
			RepliedUser: false,
		}
	}

	// Add attachments
	for _, att := range attachments {
		if att.Data != nil {
			msgSend.Files = append(msgSend.Files, &discordgo.File{
				Name:   att.Name,
				Reader: bytes.NewReader(att.Data),
	})
		} else if att.LocalPath != "" {
			file, err := os.Open(att.LocalPath)
			if err != nil {
	return err
}
			defer file.Close()
			msgSend.Files = append(msgSend.Files, &discordgo.File{
				Name:   att.Name,
				Reader: file,
			})
		}
	}

	_, err := dg.ChannelMessageSendComplex(chID, msgSend)
	return err
}

func shouldRetryDiscordSend(err error, attempt int) (bool, time.Duration) {
	if err == nil {
		return false, 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, 0
	}

	// discordgo already retries 429 by default, but handle this for safety.
	var rlErr *discordgo.RateLimitError
	if errors.As(err, &rlErr) {
		if rlErr.RetryAfter > 0 {
			return true, rlErr.RetryAfter
		}
		return true, discordSendBackoff(attempt)
	}

	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		code := restErr.Response.StatusCode
		if code == http.StatusTooManyRequests || (code >= 500 && code <= 599) {
			return true, discordSendBackoff(attempt)
		}
		return false, 0
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true, discordSendBackoff(attempt)
	}

	return false, 0
}

func discordSendBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 4)
	return 300 * time.Millisecond * time.Duration(1<<shift)
}
