package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
)

type Channel struct {
	cfg         config.TelegramConfig
	bus         *bus.Bus
	allow       channels.AllowList
	pollTimeout int

	running atomic.Bool

	mu     sync.Mutex
	cancel context.CancelFunc
	hc     *http.Client

	lastUpdateID int64
}

func New(cfg config.TelegramConfig, b *bus.Bus) *Channel {
	pollTimeout := clampTelegramPollTimeout(cfg.PollTimeoutSec)
	return &Channel{
		cfg:         cfg,
		bus:         b,
		allow:       channels.AllowList{AllowFrom: cfg.AllowFrom},
		pollTimeout: pollTimeout,
		hc: &http.Client{
			Timeout: time.Duration(pollTimeout+15) * time.Second,
		},
	}
}

func (c *Channel) Name() string    { return "telegram" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("telegram token is empty")
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		c.cancel = nil
		c.mu.Unlock()
	}()

	c.running.Store(true)
	defer c.running.Store(false)

	attempt := 1
	for {
		updates, err := c.getUpdates(runCtx, c.lastUpdateID+1)
		if err != nil {
			select {
			case <-runCtx.Done():
				return runCtx.Err()
			default:
			}

			wait := telegramPollBackoff(attempt)
			attempt++
			t := time.NewTimer(wait)
			select {
			case <-runCtx.Done():
				t.Stop()
				return runCtx.Err()
			case <-t.C:
				continue
			}
		}
		attempt = 1

		for _, up := range updates {
			if up.UpdateID > c.lastUpdateID {
				c.lastUpdateID = up.UpdateID
			}
			c.handleUpdate(runCtx, up)
		}
	}
}

func (c *Channel) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	chatID := strings.TrimSpace(msg.ChatID)
	if chatID == "" {
		return fmt.Errorf("chat_id is empty")
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return nil
	}

	req := telegramSendMessageRequest{
		ChatID: chatID,
		Text:   content,
	}
	if replyTo := resolveTelegramReplyTarget(msg); replyTo > 0 {
		req.ReplyParameters = &telegramReplyParameters{
			MessageID:                replyTo,
			AllowSendingWithoutReply: true,
		}
	}

	return c.callAPI(ctx, "sendMessage", req, nil)
}

func (c *Channel) getUpdates(ctx context.Context, offset int64) ([]telegramUpdate, error) {
	req := telegramGetUpdatesRequest{
		Offset:         offset,
		Timeout:        c.pollTimeout,
		AllowedUpdates: []string{"message", "edited_message"},
	}
	var updates []telegramUpdate
	if err := c.callAPI(ctx, "getUpdates", req, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (c *Channel) handleUpdate(ctx context.Context, up telegramUpdate) {
	msg := up.Message
	if msg == nil {
		msg = up.EditedMessage
	}
	if msg == nil || msg.From == nil {
		return
	}
	if msg.From.IsBot {
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

func (c *Channel) callAPI(ctx context.Context, method string, reqBody any, out any) error {
	baseURL := strings.TrimSpace(c.cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	token := strings.TrimSpace(c.cfg.Token)
	if token == "" {
		return fmt.Errorf("telegram token is empty")
	}
	url := strings.TrimRight(baseURL, "/") + "/bot" + token + "/" + method

	var body io.Reader = http.NoBody
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("telegram %s status %d: %s", method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var envelope telegramAPIEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("telegram %s decode response: %w", method, err)
	}
	if !envelope.OK {
		desc := strings.TrimSpace(envelope.Description)
		if desc == "" {
			desc = "unknown api error"
		}
		return fmt.Errorf("telegram %s api error: %s", method, desc)
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("telegram %s decode result: %w", method, err)
		}
	}
	return nil
}

func telegramSenderID(from *telegramUser) string {
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

func telegramMessageContent(msg *telegramMessage) string {
	if msg == nil {
		return ""
	}
	if text := strings.TrimSpace(msg.Text); text != "" {
		return text
	}
	return strings.TrimSpace(msg.Caption)
}

func buildTelegramDelivery(msg *telegramMessage) bus.Delivery {
	if msg == nil {
		return bus.Delivery{}
	}
	d := bus.Delivery{
		MessageID: strconv.Itoa(msg.MessageID),
		IsDirect:  strings.EqualFold(strings.TrimSpace(msg.Chat.Type), "private"),
	}
	if msg.ReplyToMessage != nil && msg.ReplyToMessage.MessageID > 0 {
		d.ReplyToID = strconv.Itoa(msg.ReplyToMessage.MessageID)
	}
	if msg.MessageThreadID > 0 {
		d.ThreadID = strconv.FormatInt(msg.MessageThreadID, 10)
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

func telegramPollBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 5)
	return 300 * time.Millisecond * time.Duration(1<<shift)
}

type telegramAPIEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

type telegramGetUpdatesRequest struct {
	Offset         int64    `json:"offset,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	AllowedUpdates []string `json:"allowed_updates,omitempty"`
}

type telegramSendMessageRequest struct {
	ChatID          string                   `json:"chat_id"`
	Text            string                   `json:"text"`
	ReplyParameters *telegramReplyParameters `json:"reply_parameters,omitempty"`
}

type telegramReplyParameters struct {
	MessageID                int64 `json:"message_id"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply,omitempty"`
}

type telegramUpdate struct {
	UpdateID      int64            `json:"update_id"`
	Message       *telegramMessage `json:"message,omitempty"`
	EditedMessage *telegramMessage `json:"edited_message,omitempty"`
}

type telegramMessage struct {
	MessageID       int              `json:"message_id"`
	MessageThreadID int64            `json:"message_thread_id,omitempty"`
	From            *telegramUser    `json:"from,omitempty"`
	Chat            telegramChat     `json:"chat"`
	Text            string           `json:"text,omitempty"`
	Caption         string           `json:"caption,omitempty"`
	ReplyToMessage  *telegramMessage `json:"reply_to_message,omitempty"`
}

type telegramUser struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username,omitempty"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"`
}
