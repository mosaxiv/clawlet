package telegram

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
)

type Channel struct {
	cfg   config.TelegramConfig
	bus   *bus.Bus
	allow channels.AllowList

	pollTimeoutSec int
	workers        int

	running atomic.Bool

	mu     sync.Mutex
	bot    *tgbot.Bot
	cancel context.CancelFunc
}

func New(cfg config.TelegramConfig, b *bus.Bus) *Channel {
	return &Channel{
		cfg:            cfg,
		bus:            b,
		allow:          channels.AllowList{AllowFrom: cfg.AllowFrom},
		pollTimeoutSec: clampTelegramPollTimeout(cfg.PollTimeoutSec),
		workers:        clampTelegramWorkers(cfg.Workers),
	}
}

func (c *Channel) Name() string    { return "telegram" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	token := strings.TrimSpace(c.cfg.Token)
	if token == "" {
		return fmt.Errorf("telegram token is empty")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	hc := &http.Client{Timeout: time.Duration(c.pollTimeoutSec+15) * time.Second}
	opts := []tgbot.Option{
		tgbot.WithHTTPClient(time.Duration(c.pollTimeoutSec)*time.Second, hc),
		tgbot.WithWorkers(c.workers),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{
			models.AllowedUpdateMessage,
			models.AllowedUpdateEditedMessage,
		}),
		tgbot.WithDefaultHandler(c.onUpdate),
	}
	if baseURL := strings.TrimSpace(c.cfg.BaseURL); baseURL != "" {
		opts = append(opts, tgbot.WithServerURL(baseURL))
	}

	b, err := tgbot.New(token, opts...)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.bot = b
	c.cancel = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.bot == b {
			c.bot = nil
		}
		c.cancel = nil
		c.mu.Unlock()
	}()

	c.running.Store(true)
	defer c.running.Store(false)

	b.Start(runCtx)
	return runCtx.Err()
}

func (c *Channel) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.bot = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return nil
	}

	chatIDAny, err := parseTelegramChatID(msg.ChatID)
	if err != nil {
		return err
	}

	c.mu.Lock()
	b := c.bot
	c.mu.Unlock()
	if b == nil {
		return fmt.Errorf("telegram not connected")
	}

	params := &tgbot.SendMessageParams{
		ChatID: chatIDAny,
		Text:   text,
	}
	if replyTo := resolveTelegramReplyTarget(msg); replyTo > 0 {
		params.ReplyParameters = &models.ReplyParameters{
			MessageID:                int(replyTo),
			AllowSendingWithoutReply: true,
		}
	}

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		_, err = b.SendMessage(ctx, params)
		if err == nil {
			return nil
		}
		retry, wait := shouldRetryTelegramSend(err, attempt)
		if !retry || attempt == maxAttempts {
			return err
		}
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

func (c *Channel) onUpdate(ctx context.Context, _ *tgbot.Bot, up *models.Update) {
	if up == nil {
		return
	}
	msg := up.Message
	if msg == nil {
		msg = up.EditedMessage
	}
	if msg == nil || msg.From == nil || msg.From.IsBot {
		return
	}

	senderID := telegramSenderID(msg.From)
	if !c.allow.Allowed(senderID) {
		return
	}

	content := telegramMessageContent(msg)
	if content == "" {
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   senderID,
		ChatID:     chatID,
		Content:    content,
		SessionKey: "telegram:" + chatID,
		Delivery:   buildTelegramDelivery(msg),
	})
}

func parseTelegramChatID(v string) (any, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, fmt.Errorf("chat_id is empty")
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, nil
	}
	return v, nil
}

func shouldRetryTelegramSend(err error, attempt int) (bool, time.Duration) {
	if err == nil {
		return false, 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, 0
	}

	var tooMany *tgbot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		if tooMany.RetryAfter > 0 {
			return true, time.Duration(tooMany.RetryAfter) * time.Second
		}
		return true, telegramSendBackoff(attempt)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true, telegramSendBackoff(attempt)
	}

	if isTelegram5xxError(err) {
		return true, telegramSendBackoff(attempt)
	}
	return false, 0
}

func isTelegram5xxError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	idx := strings.Index(msg, "sendMessage, ")
	if idx < 0 {
		return false
	}
	rest := msg[idx+len("sendMessage, "):]
	if len(rest) < 3 {
		return false
	}
	code, convErr := strconv.Atoi(rest[:3])
	if convErr != nil {
		return false
	}
	return code >= 500 && code <= 599
}

func telegramSendBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 4)
	return 300 * time.Millisecond * time.Duration(1<<shift)
}

func telegramSenderID(from *models.User) string {
	if from == nil {
		return ""
	}
	id := strconv.FormatInt(from.ID, 10)
	username := strings.TrimPrefix(strings.TrimSpace(from.Username), "@")
	if username == "" {
		return id
	}
	return id + "|" + username
}

func telegramMessageContent(msg *models.Message) string {
	if msg == nil {
		return ""
	}
	if text := strings.TrimSpace(msg.Text); text != "" {
		return text
	}
	return strings.TrimSpace(msg.Caption)
}

func buildTelegramDelivery(msg *models.Message) bus.Delivery {
	if msg == nil {
		return bus.Delivery{}
	}
	d := bus.Delivery{
		MessageID: strconv.Itoa(msg.ID),
		IsDirect:  msg.Chat.Type == models.ChatTypePrivate,
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.ID > 0 {
		d.ReplyToID = strconv.Itoa(msg.ReplyToMessage.ID)
	}
	if msg.MessageThreadID > 0 {
		d.ThreadID = strconv.Itoa(msg.MessageThreadID)
	}
	return d
}

func resolveTelegramReplyTarget(msg bus.OutboundMessage) int64 {
	candidates := []string{
		strings.TrimSpace(msg.Delivery.ReplyToID),
		strings.TrimSpace(msg.ReplyTo),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		n, err := strconv.ParseInt(c, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func clampTelegramPollTimeout(v int) int {
	if v <= 0 {
		return 25
	}
	if v > 50 {
		return 50
	}
	return v
}

func clampTelegramWorkers(v int) int {
	if v <= 0 {
		return 2
	}
	if v > 8 {
		return 8
	}
	return v
}
