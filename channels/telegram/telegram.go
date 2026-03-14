package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
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
	_, _ = b.DeleteWebhook(runCtx, &tgbot.DeleteWebhookParams{DropPendingUpdates: true})

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
	attachments := msg.Attachments
	if text == "" && len(attachments) == 0 {
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

	// Send text message if present
	if text != "" {
	params := &tgbot.SendMessageParams{
		ChatID:    chatIDAny,
		Text:      markdownToTelegramHTML(text),
		ParseMode: models.ParseModeHTML,
	}
	if replyTo := resolveTelegramReplyTarget(msg); replyTo > 0 {
		params.ReplyParameters = &models.ReplyParameters{
			MessageID:                int(replyTo),
			AllowSendingWithoutReply: true,
		}
	}
	if err := c.sendMessageWithRetry(ctx, b, params); err == nil {
			// Continue to send attachments
	} else if !isTelegramParseError(err) {
		return err
		} else {
	params.Text = text
	params.ParseMode = ""
			if err := c.sendMessageWithRetry(ctx, b, params); err != nil {
				return err
}
		}
	}

	// Send attachments
	for _, att := range attachments {
		if err := c.sendTelegramAttachment(ctx, b, chatIDAny, att, msg); err != nil {
			return err
		}
	}

	return nil
}

func (c *Channel) sendTelegramAttachment(ctx context.Context, b *tgbot.Bot, chatID any, att bus.Attachment, msg bus.OutboundMessage) error {
	var inputFile models.InputFile

	if att.Data != nil {
		// Use in-memory data
		inputFile = &models.InputFileUpload{
			Filename: att.Name,
			Data:     bytes.NewReader(att.Data),
		}
	} else if att.LocalPath != "" {
		// Use local file path
		file, err := os.Open(att.LocalPath)
		if err != nil {
			return err
		}
		defer file.Close()
		inputFile = &models.InputFileUpload{
			Filename: att.Name,
			Data:     file,
		}
	} else {
		return fmt.Errorf("attachment has no data or local path")
	}

	replyTo := resolveTelegramReplyTarget(msg)

	// Determine send method based on attachment kind or MIME type
	switch strings.ToLower(att.Kind) {
	case "image":
		params := &tgbot.SendPhotoParams{
			ChatID:  chatID,
			Photo:   inputFile,
			Caption: att.Name,
		}
		if replyTo > 0 {
			params.ReplyParameters = &models.ReplyParameters{
				MessageID:                int(replyTo),
				AllowSendingWithoutReply: true,
			}
		}
		_, err := b.SendPhoto(ctx, params)
		return err
	case "audio":
		params := &tgbot.SendAudioParams{
			ChatID:  chatID,
			Audio:   inputFile,
			Caption: att.Name,
		}
		if replyTo > 0 {
			params.ReplyParameters = &models.ReplyParameters{
				MessageID:                int(replyTo),
				AllowSendingWithoutReply: true,
			}
		}
		_, err := b.SendAudio(ctx, params)
		return err
	case "video":
		params := &tgbot.SendVideoParams{
			ChatID:  chatID,
			Video:   inputFile,
			Caption: att.Name,
		}
		if replyTo > 0 {
			params.ReplyParameters = &models.ReplyParameters{
				MessageID:                int(replyTo),
				AllowSendingWithoutReply: true,
			}
		}
		_, err := b.SendVideo(ctx, params)
		return err
	default:
		// Send as document for all other types
		params := &tgbot.SendDocumentParams{
			ChatID:   chatID,
			Document: inputFile,
			Caption:  att.Name,
		}
		if replyTo > 0 {
			params.ReplyParameters = &models.ReplyParameters{
				MessageID:                int(replyTo),
				AllowSendingWithoutReply: true,
			}
		}
		_, err := b.SendDocument(ctx, params)
		return err
	}
}

func (c *Channel) onUpdate(ctx context.Context, b *tgbot.Bot, up *models.Update) {
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
	attachments := c.telegramInboundAttachments(ctx, b, msg)
	if content == "" && len(attachments) == 0 {
		return
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	c.sendTypingHint(chatID)
	// Avoid blocking telegram worker goroutines indefinitely when bus is saturated.
	publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = c.bus.PublishInbound(publishCtx, bus.InboundMessage{
		Channel:     "telegram",
		SenderID:    senderID,
		ChatID:      chatID,
		Content:     content,
		Attachments: attachments,
		SessionKey:  "telegram:" + chatID,
		Delivery:    buildTelegramDelivery(msg),
	})
	cancel()
}

func (c *Channel) sendMessageWithRetry(ctx context.Context, b *tgbot.Bot, params *tgbot.SendMessageParams) error {
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		_, err := b.SendMessage(ctx, params)
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

func (c *Channel) sendTypingHint(chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	chatIDAny, err := parseTelegramChatID(chatID)
	if err != nil {
		return
	}

	c.mu.Lock()
	b := c.bot
	c.mu.Unlock()
	if b == nil {
		return
	}

	go func(bot *tgbot.Bot, id any) {
		typingCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		_, _ = bot.SendChatAction(typingCtx, &tgbot.SendChatActionParams{
			ChatID: id,
			Action: models.ChatActionTyping,
		})
	}(b, chatIDAny)
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
	_, after, ok := strings.Cut(msg, "sendMessage, ")
	if !ok {
		return false
	}
	rest := after
	if len(rest) < 3 {
		return false
	}
	code, convErr := strconv.Atoi(rest[:3])
	if convErr != nil {
		return false
	}
	return code >= 500 && code <= 599
}

func isTelegramParseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "can't parse entities") ||
		(strings.Contains(msg, "parse entities") && strings.Contains(msg, " 400 "))
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

func (c *Channel) telegramInboundAttachments(ctx context.Context, b *tgbot.Bot, msg *models.Message) []bus.Attachment {
	if msg == nil || b == nil {
		return nil
	}
	candidates := make([]telegramFileRef, 0, 5)
	if len(msg.Photo) > 0 {
		p := msg.Photo[len(msg.Photo)-1]
		candidates = append(candidates, telegramFileRef{
			ID:       p.FileID,
			Name:     "photo.jpg",
			MIMEType: "image/jpeg",
			Kind:     "image",
			Size:     int64(p.FileSize),
		})
	}
	if msg.Audio != nil {
		candidates = append(candidates, telegramFileRef{
			ID:       msg.Audio.FileID,
			Name:     fallbackTelegramName(msg.Audio.FileName, "audio"),
			MIMEType: msg.Audio.MimeType,
			Kind:     "audio",
			Size:     msg.Audio.FileSize,
		})
	}
	if msg.Voice != nil {
		candidates = append(candidates, telegramFileRef{
			ID:       msg.Voice.FileID,
			Name:     "voice.ogg",
			MIMEType: fallbackTelegramMime(msg.Voice.MimeType, "audio/ogg"),
			Kind:     "audio",
			Size:     msg.Voice.FileSize,
		})
	}
	if msg.Video != nil {
		candidates = append(candidates, telegramFileRef{
			ID:       msg.Video.FileID,
			Name:     fallbackTelegramName(msg.Video.FileName, "video"),
			MIMEType: msg.Video.MimeType,
			Kind:     "video",
			Size:     msg.Video.FileSize,
		})
	}
	if msg.Document != nil {
		candidates = append(candidates, telegramFileRef{
			ID:       msg.Document.FileID,
			Name:     fallbackTelegramName(msg.Document.FileName, "document"),
			MIMEType: msg.Document.MimeType,
			Kind:     bus.InferAttachmentKind(msg.Document.MimeType),
			Size:     msg.Document.FileSize,
		})
	}

	out := make([]bus.Attachment, 0, len(candidates))
	for _, cand := range candidates {
		cand.ID = strings.TrimSpace(cand.ID)
		if cand.ID == "" {
			continue
		}
		fileURL, err := c.resolveTelegramFileURL(ctx, b, cand.ID)
		if err != nil || strings.TrimSpace(fileURL) == "" {
			continue
		}
		mimeType := strings.TrimSpace(cand.MIMEType)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		kind := strings.TrimSpace(cand.Kind)
		if kind == "" {
			kind = bus.InferAttachmentKind(mimeType)
		}
		out = append(out, bus.Attachment{
			ID:        cand.ID,
			Name:      strings.TrimSpace(cand.Name),
			MIMEType:  mimeType,
			Kind:      kind,
			SizeBytes: cand.Size,
			URL:       fileURL,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type telegramFileRef struct {
	ID       string
	Name     string
	MIMEType string
	Kind     string
	Size     int64
}

func fallbackTelegramName(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return fallback
}

func fallbackTelegramMime(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return fallback
}

func (c *Channel) resolveTelegramFileURL(ctx context.Context, b *tgbot.Bot, fileID string) (string, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", fmt.Errorf("telegram file id is empty")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := b.GetFile(reqCtx, &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return "", err
	}
	if res == nil || strings.TrimSpace(res.FilePath) == "" {
		return "", fmt.Errorf("telegram file path is empty")
	}
	return telegramFileURL(c.cfg.BaseURL, c.cfg.Token, res.FilePath)
}

func telegramFileURL(baseURL, token, filePath string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("telegram token is empty")
	}
	filePath = strings.TrimLeft(strings.TrimSpace(filePath), "/")
	if filePath == "" {
		return "", fmt.Errorf("telegram file path is empty")
	}
	return baseURL + "/file/bot" + token + "/" + filePath, nil
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
