package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
)

// Feishu API constants
const (
	feishuAPIBase = "https://open.feishu.cn"
)

// Event payload structure for im.message.receive_v1/v2
// V2 format: {"header":{...}, "event":{"sender":{...}, "message":{...}}}
// V1 format: {"header":{...}, "event":{"sender_id":{...}, "sender_type":"...", "message":{...}}}
type feishuEventPayload struct {
	Schema string               `json:"schema"`
	Header *feishuEventHeader   `json:"header"`
	Event  *feishuEventDataV1   `json:"event"`
}

type feishuEventHeader struct {
	EventType string `json:"event_type"`
	EventID   string `json:"event_id"`
	AppID     string `json:"app_id"`
	TenantKey string `json:"tenant_key"`
	Timestamp string `json:"timestamp"`
}

// V1 event data format (direct fields)
type feishuEventDataV1 struct {
	SenderID   *feishuUserID     `json:"sender_id,omitempty"`
	SenderType *string           `json:"sender_type,omitempty"`
	TenantKey  *string           `json:"tenant_key,omitempty"`
	Message    *feishuEventMessage `json:"message,omitempty"`
	// V2 format fields (nested)
	Sender  *feishuEventSender  `json:"sender,omitempty"`
}

type feishuEventSender struct {
	SenderID   *feishuUserID `json:"sender_id,omitempty"`
	SenderType *string       `json:"sender_type,omitempty"`
	TenantKey  *string       `json:"tenant_key,omitempty"`
}

type feishuUserID struct {
	UserId  *string `json:"user_id,omitempty"`
	OpenId  *string `json:"open_id,omitempty"`
	UnionId *string `json:"union_id,omitempty"`
}

type feishuEventMessage struct {
	MessageID     *string `json:"message_id,omitempty"`
	RootID        *string `json:"root_id,omitempty"`
	ParentID      *string `json:"parent_id,omitempty"`
	ChatID        *string `json:"chat_id,omitempty"`
	MsgType       *string `json:"message_type,omitempty"`  // v1 uses "message_type"
	Content       *string `json:"content,omitempty"`
	SenderID      *feishuUserID `json:"sender_id,omitempty"`
	SenderType    *string       `json:"sender_type,omitempty"`
	CreateTime    *string `json:"create_time,omitempty"`
	UpdateID      *int64  `json:"update_id,omitempty"`
	ChatType      *string `json:"chat_type,omitempty"`
	MentionAll    *bool   `json:"mention_all,omitempty"`
}

// Legacy structure for direct message parsing
type feishuMessageV2 struct {
	MessageID       string `json:"message_id"`
	RootID          string `json:"root_id"`
	ParentID        string `json:"parent_id"`
	ChatID          string `json:"chat_id"`
	MsgType         string `json:"msg_type"`
	Content         string `json:"content"`
	SenderID        string `json:"sender_id"`
	SenderType      string `json:"sender_type"`
	SenderTenantKey string `json:"sender_tenant_key"`
	CreateTime      string `json:"create_time"`
	UpdateID        int64  `json:"update_id"`
	ChatType        string `json:"chat_type"`
	MentionAll      *bool  `json:"mention_all"`
}

type feishuMessageContent struct {
	Text string `json:"text"`
}

type Channel struct {
	cfg   config.FeishuConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	mu        sync.Mutex
	conn      *websocket.Conn
	client    *http.Client
	appID     string
	appSecret string
	cancel    context.CancelFunc

	// Bot info
	botOpenID string

	// Processed message dedup
	processedIDs map[string]bool
	processedMu  sync.Mutex

	// Connection info
	connURL   *url.URL
	connID    string
	serviceID string

	// Config
	reconnectCount    int
	reconnectInterval time.Duration
	pingInterval      time.Duration
}

func New(cfg config.FeishuConfig, b *bus.Bus) *Channel {
	return &Channel{
		cfg:               cfg,
		bus:               b,
		allow:             channels.AllowList{AllowFrom: cfg.AllowFrom},
		client:            &http.Client{Timeout: 30 * time.Second},
		processedIDs:      make(map[string]bool),
		reconnectCount:    -1,  // unlimited
		reconnectInterval: 120 * time.Second,
		pingInterval:      120 * time.Second,
	}
}

func (c *Channel) Name() string    { return "feishu" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.AppID) == "" {
		return fmt.Errorf("feishu appID is empty")
	}
	if strings.TrimSpace(c.cfg.AppSecret) == "" {
		return fmt.Errorf("feishu appSecret is empty")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.mu.Lock()
	c.appID = strings.TrimSpace(c.cfg.AppID)
	c.appSecret = strings.TrimSpace(c.cfg.AppSecret)
	c.cancel = cancel
	c.mu.Unlock()

	c.running.Store(true)
	defer c.running.Store(false)

	// Fetch bot open_id
	c.fetchBotOpenID(runCtx)

	// Connect to WebSocket
	if err := c.connect(runCtx); err != nil {
		return fmt.Errorf("failed to connect to feishu websocket: %w", err)
	}

	log.Printf("feishu channel started (app_id=%s)\n", c.appID[:min(12, len(c.appID))])

	return runCtx.Err()
}

func (c *Channel) Stop() error {
	c.running.Store(false)
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
	return nil
}

func (c *Channel) connect(ctx context.Context) error {
	// Get WebSocket URL
	connURL, err := c.getConnURL(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection URL: %w", err)
	}

	u, err := url.Parse(connURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	connID := u.Query().Get("device_id")
	serviceID := u.Query().Get("service_id")

	log.Printf("feishu: got websocket endpoint (conn_id=%s, service_id=%s)\n", connID, serviceID)

	// Connect
	conn, _, err := websocket.DefaultDialer.Dial(connURL, nil)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.connURL = u
	c.connID = connID
	c.serviceID = serviceID
	c.mu.Unlock()

	log.Println("feishu: websocket connected")

	// Start ping loop
	go c.pingLoop(ctx)

	// Run receive loop in current goroutine (don't return)
	c.receiveMessageLoop(ctx)

	return nil
}

func (c *Channel) getConnURL(ctx context.Context) (string, error) {
	body := map[string]string{
		"AppID":     c.appID,
		"AppSecret": c.appSecret,
	}
	bs, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, feishuAPIBase+"/callback/ws/endpoint", bytes.NewBuffer(bs))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("locale", "zh")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code int `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			URL          string `json:"url"`
			ClientConfig *struct {
				ReconnectCount    int `json:"reconnect_count"`
				ReconnectInterval int `json:"reconnect_interval"`
				ReconnectNonce    int `json:"reconnect_nonce"`
				PingInterval      int `json:"ping_interval"`
			} `json:"client_config"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Code != 0 {
		return "", fmt.Errorf("feishu endpoint error: code=%d, msg=%s", result.Code, result.Msg)
	}

	if result.Data == nil || result.Data.URL == "" {
		return "", fmt.Errorf("feishu endpoint returned empty URL")
	}

	if result.Data.ClientConfig != nil {
		c.reconnectCount = result.Data.ClientConfig.ReconnectCount
		c.reconnectInterval = time.Duration(result.Data.ClientConfig.ReconnectInterval) * time.Second
		c.pingInterval = time.Duration(result.Data.ClientConfig.PingInterval) * time.Second
	}

	return result.Data.URL, nil
}

func (c *Channel) receiveMessageLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			log.Println("feishu: receiveMessageLoop: context done")
			return
		default:
		}

		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			log.Println("feishu: receiveMessageLoop: connection is nil")
			return
		}

		mt, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("feishu: receiveMessageLoop: read error: %v\n", err)
			c.disconnect(ctx)
			go c.reconnect(ctx)
			return
		}

		log.Printf("feishu: <<< received WS message: type=%d, len=%d\n", mt, len(msg))

		if mt != websocket.BinaryMessage {
			log.Printf("feishu: received non-binary message type: %d\n", mt)
			continue
		}

		go c.handleMessage(ctx, msg)
	}
}

func (c *Channel) handleMessage(ctx context.Context, msg []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("feishu: handleMessage panic: %v\n", r)
		}
	}()

	log.Printf("feishu: handling message (%d bytes): % x\n", len(msg), msg[:min(32, len(msg))])

	// Parse frame using official SDK
	var frame ws.Frame
	if err := frame.Unmarshal(msg); err != nil {
		log.Printf("feishu: unmarshal frame failed: %v\n", err)
		return
	}

	log.Printf("feishu: parsed frame: method=%d, service=%d, len(headers)=%d, len(payload)=%d\n",
		frame.Method, frame.Service, len(frame.Headers), len(frame.Payload))

	// Handle based on frame type
	if frame.Method == 0 { // CONTROL
		log.Println("feishu: handling CONTROL frame")
		c.handleControlFrame(ctx, frame)
	} else if frame.Method == 1 { // DATA
		log.Println("feishu: handling DATA frame")
		c.handleDataFrame(ctx, frame)
	} else {
		log.Printf("feishu: unknown frame method: %d\n", frame.Method)
	}
}

func (c *Channel) handleControlFrame(ctx context.Context, frame ws.Frame) {
	hs := ws.Headers(frame.Headers)
	t := hs.GetString("type")

	if t == "pong" {
		log.Println("feishu: received pong")
		if len(frame.Payload) > 0 {
			var conf struct {
				PingInterval      int `json:"ping_interval"`
				ReconnectCount    int `json:"reconnect_count"`
				ReconnectInterval int `json:"reconnect_interval"`
			}
			if err := json.Unmarshal(frame.Payload, &conf); err == nil {
				if conf.PingInterval > 0 {
					c.pingInterval = time.Duration(conf.PingInterval) * time.Second
				}
			}
		}
	}
}

func (c *Channel) handleDataFrame(ctx context.Context, frame ws.Frame) {
	hs := ws.Headers(frame.Headers)
	msgID := hs.GetString("message_id")
	traceID := hs.GetString("trace_id")
	type_ := hs.GetString("type")

	log.Printf("feishu: received data frame: type=%s, msg_id=%s, trace_id=%s\n", type_, msgID, traceID)

	if type_ != "event" {
		log.Printf("feishu: skipping non-event frame\n")
		return
	}

	// Parse event payload
	var payload feishuEventPayload
	if err := json.Unmarshal(frame.Payload, &payload); err != nil {
		log.Printf("feishu: unmarshal event payload failed: %v\n", err)
		return
	}

	log.Printf("feishu: event type: %s\n", payload.Header.EventType)

	// Handle message receive events
	if payload.Header.EventType == "im.message.receive_v1" || payload.Header.EventType == "im.message.receive_v2" {
		log.Printf("feishu: processing %s event\n", payload.Header.EventType)

		if payload.Event == nil {
			log.Println("feishu: event is nil")
			return
		}
		if payload.Event.Message == nil {
			log.Println("feishu: event.Message is nil")
			return
		}
		msg := payload.Event.Message

		// Get sender ID from event level (v1) or message level (v2)
		var senderIDStr string
		if payload.Event.SenderID != nil && payload.Event.SenderID.OpenId != nil {
			senderIDStr = *payload.Event.SenderID.OpenId
		} else if msg.SenderID != nil && msg.SenderID.OpenId != nil {
			senderIDStr = *msg.SenderID.OpenId
		}

		// Safe access with nil checks
		chatID := ""
		if msg.ChatID != nil {
			chatID = *msg.ChatID
		}
		msgType := ""
		if msg.MsgType != nil {
			msgType = *msg.MsgType
		}
		content := ""
		if msg.Content != nil {
			content = *msg.Content
		}
		chatType := ""
		if msg.ChatType != nil {
			chatType = *msg.ChatType
		}

		log.Printf("feishu: received message: chatID=%s, senderID=%s, msgType=%s, chatType=%s, content=%s\n",
			chatID, senderIDStr, msgType, chatType, content)

		// Process the message
		c.processMessageV2(ctx, msg, frame.Payload, hs, senderIDStr)
	} else {
		log.Printf("feishu: unhandled event type: %s\n", payload.Header.EventType)
	}
}

func (c *Channel) processMessageV2(ctx context.Context, msg *feishuEventMessage, rawPayload []byte, hs ws.Headers, senderID string) {
	if msg == nil {
		log.Println("feishu: message is nil")
		return
	}

	// Extract values with nil checks
	messageID := ""
	if msg.MessageID != nil {
		messageID = *msg.MessageID
	}
	chatID := ""
	if msg.ChatID != nil {
		chatID = *msg.ChatID
	}
	msgType := ""
	if msg.MsgType != nil {
		msgType = *msg.MsgType
	}
	content := ""
	if msg.Content != nil {
		content = *msg.Content
	}
	chatType := ""
	if msg.ChatType != nil {
		chatType = *msg.ChatType
	}
	rootID := ""
	if msg.RootID != nil {
		rootID = *msg.RootID
	}
	parentID := ""
	if msg.ParentID != nil {
		parentID = *msg.ParentID
	}

	log.Printf("feishu: processing message: ID=%s, chatID=%s, senderID=%s, msgType=%s\n", messageID, chatID, senderID, msgType)

	// Dedup check
	c.processedMu.Lock()
	if c.processedIDs[messageID] {
		c.processedMu.Unlock()
		log.Printf("feishu: duplicate message %s, skipping\n", messageID)
		return
	}
	c.processedIDs[messageID] = true
	if len(c.processedIDs) > 1000 {
		for k := range c.processedIDs {
			delete(c.processedIDs, k)
			if len(c.processedIDs) <= 500 {
				break
			}
		}
	}
	c.processedMu.Unlock()

	// Ignore bot's own messages
	if c.botOpenID != "" && senderID == c.botOpenID {
		log.Println("feishu: ignoring bot's own message")
		return
	}

	// Handle different message types
	isDirect := chatType == "p2p" || strings.HasPrefix(chatID, "oc_")
	switch msgType {
	case "text":
		c.processTextMessage(ctx, content, senderID, chatID, messageID, rootID, parentID, isDirect)
	case "image":
		c.processImageMessage(ctx, content, senderID, chatID, messageID, rootID, parentID, isDirect)
	case "file":
		c.processFileMessage(ctx, content, senderID, chatID, messageID, rootID, parentID, isDirect)
	default:
		log.Printf("feishu: unsupported message type: %s\n", msgType)
	}
}

func (c *Channel) processTextMessage(ctx context.Context, content, senderID, chatID, messageID, rootID, parentID string, isDirect bool) {
	var msgContent feishuMessageContent
	if err := json.Unmarshal([]byte(content), &msgContent); err != nil {
		log.Printf("feishu: failed to parse content: %v\n", err)
		return
	}

	text := strings.TrimSpace(msgContent.Text)
	log.Printf("feishu: message text: %s\n", text)
	if text == "" {
		log.Println("feishu: empty message text")
		return
	}

	// Check allowlist
	if !c.allow.Allowed(senderID) {
		log.Printf("feishu: sender %s not in allowlist\n", senderID)
		return
	}

	// Strip bot @mention
	text = c.stripBotMention(text)
	log.Printf("feishu: after strip mention: %s\n", text)
	if text == "" {
		log.Println("feishu: empty text after stripping mentions")
		return
	}

	// Publish to bus
	log.Println("feishu: publishing to bus")
	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:    "feishu",
		SenderID:   senderID,
		ChatID:     chatID,
		Content:    text,
		SessionKey: "feishu:" + chatID,
		Delivery: bus.Delivery{
			MessageID: messageID,
			ThreadID:  rootID,
			ReplyToID: parentID,
			IsDirect:  isDirect,
		},
	})

	// Send acknowledgment response to Feishu
	c.sendEventAck(ctx, messageID, "")
}

func (c *Channel) processImageMessage(ctx context.Context, content, senderID, chatID, messageID, rootID, parentID string, isDirect bool) {
	// Parse image content - format: {"image_key":"img_xxx"}
	var imgContent struct {
		ImageKey string `json:"image_key"`
	}
	if err := json.Unmarshal([]byte(content), &imgContent); err != nil {
		log.Printf("feishu: failed to parse image content: %v, raw: %s\n", err, content)
		return
	}

	if imgContent.ImageKey == "" {
		log.Println("feishu: empty image_key")
		return
	}

	log.Printf("feishu: received image: image_key=%s\n", imgContent.ImageKey)

	// Check allowlist
	if !c.allow.Allowed(senderID) {
		log.Printf("feishu: sender %s not in allowlist\n", senderID)
		return
	}

	// Download image to local workspace (optional - continue even if download fails)
	localPath := ""
	downloadedPath, err := c.downloadImage(ctx, imgContent.ImageKey, chatID)
	if err != nil {
		log.Printf("feishu: failed to download image: %v (continuing without local file)\n", err)
	} else {
		localPath = downloadedPath
	}

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "feishu",
		SenderID: senderID,
		ChatID:   chatID,
		Content:  "[Image]",
		Attachments: []bus.Attachment{
			{
				ID:        imgContent.ImageKey,
				Name:      "image.jpg",
				MIMEType:  "image/jpeg",
				Kind:      "image",
				LocalPath: localPath,
			},
		},
		SessionKey: "feishu:" + chatID,
		Delivery: bus.Delivery{
			MessageID: messageID,
			ThreadID:  rootID,
			ReplyToID: parentID,
			IsDirect:  isDirect,
		},
	})

	// Send acknowledgment response to Feishu
	c.sendEventAck(ctx, messageID, "")
	log.Println("feishu: image message published to bus")
}

func (c *Channel) processFileMessage(ctx context.Context, content, senderID, chatID, messageID, rootID, parentID string, isDirect bool) {
	var fileContent struct {
		FileKey string `json:"file_key"`
		FileName string `json:"file_name"`
	}
	if err := json.Unmarshal([]byte(content), &fileContent); err != nil {
		log.Printf("feishu: failed to parse file content: %v\n", err)
		return
	}

	if fileContent.FileKey == "" {
		log.Println("feishu: empty file_key")
		return
	}

	log.Printf("feishu: received file: file_key=%s\n", fileContent.FileKey)

	// Check allowlist
	if !c.allow.Allowed(senderID) {
		log.Printf("feishu: sender %s not in allowlist\n", senderID)
		return
	}

	// Download file to local workspace (optional - continue even if download fails)
	localPath := ""
	downloadedPath, err := c.downloadFile(ctx, fileContent.FileKey, chatID, fileContent.FileName)
	if err != nil {
		log.Printf("feishu: failed to download file: %v (continuing without local file)\n", err)
	} else {
		localPath = downloadedPath
	}

	fileName := fileContent.FileName
	if fileName == "" {
		fileName = "file"
	}

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:  "feishu",
		SenderID: senderID,
		ChatID:   chatID,
		Content:  "[File]",
		Attachments: []bus.Attachment{
			{
				ID:        fileContent.FileKey,
				Name:      fileName,
				MIMEType:  "application/octet-stream",
				Kind:      "file",
				LocalPath: localPath,
			},
		},
		SessionKey: "feishu:" + chatID,
		Delivery: bus.Delivery{
			MessageID: messageID,
			ThreadID:  rootID,
			ReplyToID: parentID,
			IsDirect:  isDirect,
		},
	})

	// Send acknowledgment response to Feishu
	c.sendEventAck(ctx, messageID, "")
	log.Println("feishu: file message published to bus")
}

func (c *Channel) publishMessageToBus(ctx context.Context, senderID, chatID, text, messageID, rootID, parentID string, isDirect bool) {
	// Check allowlist
	if !c.allow.Allowed(senderID) {
		log.Printf("feishu: sender %s not in allowlist\n", senderID)
		return
	}

	// Strip bot @mention
	text = c.stripBotMention(text)
	log.Printf("feishu: after strip mention: %s\n", text)
	if text == "" {
		log.Println("feishu: empty text after stripping mentions")
		return
	}

	// Determine if direct message
	isDirect = isDirect || strings.HasPrefix(chatID, "oc_")
	log.Printf("feishu: isDirect=%v\n", isDirect)

	// Publish to bus
	log.Println("feishu: publishing to bus")
	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:    "feishu",
		SenderID:   senderID,
		ChatID:     chatID,
		Content:    text,
		SessionKey: "feishu:" + chatID,
		Delivery: bus.Delivery{
			MessageID: messageID,
			ThreadID:  rootID,
			ReplyToID: parentID,
			IsDirect:  isDirect,
		},
	})
}

// sendEventAck sends an acknowledgment response to Feishu for event processing
func (c *Channel) sendEventAck(ctx context.Context, msgID, traceID string) {
	c.mu.Lock()
	conn := c.conn
	serviceID := c.serviceID
	c.mu.Unlock()

	if conn == nil || serviceID == "" {
		return
	}

	sid := int32(0)
	_, _ = fmt.Sscanf(serviceID, "%d", &sid)

	// Build response frame
	response := map[string]interface{}{
		"code": 0, // HTTP 200 OK
	}
	responseJSON, _ := json.Marshal(response)

	frame := &ws.Frame{
		SeqID:   0,
		LogID:   0,
		Service: sid,
		Method:  1, // DATA frame
		Headers: []ws.Header{
			{Key: "message_id", Value: msgID},
			{Key: "trace_id", Value: traceID},
			{Key: "type", Value: "event"},
		},
		Payload: responseJSON,
	}

	data, _ := frame.Marshal()
	if err := conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		log.Printf("feishu: failed to send event ack: %v\n", err)
	} else {
		log.Println("feishu: sent event acknowledgment")
	}
}

func (c *Channel) stripBotMention(text string) string {
	text = strings.TrimSpace(text)

	for {
		start := strings.Index(text, "<at")
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], "</at>")
		if end == -1 {
			break
		}
		end += start + 5
		mention := text[start:end]

		if strings.Contains(mention, `user_id="`) {
			text = strings.Replace(text, mention, "", 1)
			text = strings.TrimSpace(text)
			text = strings.TrimLeft(text, ":,，.。:：")
			text = strings.TrimSpace(text)
			continue
		}
		break
	}
	return text
}

func (c *Channel) pingLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("feishu: ping loop panic: %v\n", r)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.pingInterval):
			c.sendPing(ctx)
		}
	}
}

func (c *Channel) sendPing(ctx context.Context) {
	c.mu.Lock()
	conn := c.conn
	serviceID := c.serviceID
	c.mu.Unlock()

	if conn == nil || serviceID == "" {
		return
	}

	i := int32(0)
	_, _ = fmt.Sscanf(serviceID, "%d", &i)

	frame := ws.NewPingFrame(i)
	bs, _ := frame.Marshal()

	if err := conn.WriteMessage(websocket.BinaryMessage, bs); err != nil {
		log.Printf("feishu: ping failed: %v\n", err)
	} else {
		log.Println("feishu: sent ping")
	}
}

func (c *Channel) reconnect(ctx context.Context) {
	if !c.running.Load() {
		return
	}

	log.Println("feishu: attempting to reconnect...")

	// Random nonce for first reconnect
	if c.reconnectCount < 0 || c.reconnectCount > 0 {
		time.Sleep(time.Duration(randInt(0, 30000)) * time.Millisecond)
	}

	count := 0
	for c.reconnectCount < 0 || count < c.reconnectCount {
		if err := c.connect(ctx); err != nil {
			log.Printf("feishu: reconnect failed: %v\n", err)
			count++
			time.Sleep(c.reconnectInterval)
			continue
		}
		return
	}

	log.Printf("feishu: reconnect failed after %d attempts\n", count)
}

func (c *Channel) disconnect(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return
	}

	c.conn.Close()
	log.Printf("feishu: disconnected from %s\n", c.connURL)

	c.conn = nil
	c.connURL = nil
	c.connID = ""
	c.serviceID = ""
}

func (c *Channel) getImageURL(ctx context.Context, imageKey string) (string, error) {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return "", err
	}

	// Get image download URL
	url := fmt.Sprintf("%s/open-apis/im/v1/images/%s", feishuAPIBase, imageKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageKey string `json:"image_key"`
			ImageURL string `json:"image_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Code != 0 {
		return "", fmt.Errorf("feishu API error: code=%d, msg=%s", result.Code, result.Msg)
	}

	return result.Data.ImageURL, nil
}

func (c *Channel) downloadImage(ctx context.Context, imageKey, chatID string) (string, error) {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return "", err
	}

	// Get image download URL
	url := fmt.Sprintf("%s/open-apis/im/v1/images/%s", feishuAPIBase, imageKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: status=%d", resp.StatusCode)
	}

	// Read image data
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Save to workspace directory
	filename := fmt.Sprintf("feishu_image_%s_%s.jpg", chatID, imageKey)
	localPath, err := c.saveToWorkspace(filename, data)
	if err != nil {
		return "", err
	}

	log.Printf("feishu: image saved to %s\n", localPath)
	return localPath, nil
}

func (c *Channel) downloadFile(ctx context.Context, fileKey, chatID, fileName string) (string, error) {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return "", err
	}

	// Get file download URL
	url := fmt.Sprintf("%s/open-apis/im/v1/files/%s", feishuAPIBase, fileKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file: status=%d", resp.StatusCode)
	}

	// Read file data
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Save to workspace directory
	if fileName == "" {
		fileName = fmt.Sprintf("feishu_file_%s_%s", chatID, fileKey)
	} else {
		fileName = fmt.Sprintf("feishu_%s_%s", chatID, fileName)
	}
	localPath, err := c.saveToWorkspace(fileName, data)
	if err != nil {
		return "", err
	}

	log.Printf("feishu: file saved to %s\n", localPath)
	return localPath, nil
}

func (c *Channel) saveToWorkspace(filename string, data []byte) (string, error) {
	// Get workspace directory (use current directory or CLAWLET_WORKSPACE env)
	workspace := os.Getenv("CLAWLET_WORKSPACE")
	if workspace == "" {
		workspace = "."
	}

	// Create feishu subdirectory
	feishuDir := filepath.Join(workspace, "feishu_files")
	if err := os.MkdirAll(feishuDir, 0755); err != nil {
		return "", err
	}

	// Sanitize filename
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, "..", "_")

	localPath := filepath.Join(feishuDir, filename)
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return "", err
	}

	return localPath, nil
}

func (c *Channel) fetchBotOpenID(ctx context.Context) {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		log.Printf("feishu: failed to get token for bot info: %v\n", err)
		return
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, feishuAPIBase+"/open-apis/bot/v3/info", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	if result.Code == 0 && result.Bot.OpenID != "" {
		c.botOpenID = result.Bot.OpenID
		log.Printf("feishu: bot open_id=%s\n", c.botOpenID[:min(12, len(c.botOpenID))])
	}
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	log.Printf("feishu: Send called: channel=%s, chatID=%s, content=%s, attachments=%d\n",
		msg.Channel, msg.ChatID, msg.Content, len(msg.Attachments))

	text := strings.TrimSpace(msg.Content)
	attachments := msg.Attachments

	// If only attachments, send them
	if text == "" && len(attachments) > 0 {
		log.Printf("feishu: sending only attachments\n")
		for i, att := range attachments {
			log.Printf("feishu: attachment[%d]: name=%s, kind=%s, localPath=%s, hasData=%v\n",
				i, att.Name, att.Kind, att.LocalPath, att.Data != nil)
			if err := c.sendAttachment(ctx, msg.ChatID, att, msg); err != nil {
				log.Printf("feishu: failed to send attachment: %v\n", err)
				return err
			}
		}
		return nil
	}

	// If only text, send text
	if text != "" && len(attachments) == 0 {
		log.Printf("feishu: sending only text\n")
		return c.sendTextMessage(ctx, msg.ChatID, text, msg)
	}

	// If both text and attachments, send text first then attachments
	if text != "" {
		log.Printf("feishu: sending text first\n")
		if err := c.sendTextMessage(ctx, msg.ChatID, text, msg); err != nil {
			return err
		}
	}

	for i, att := range attachments {
		log.Printf("feishu: sending attachment[%d]: name=%s, kind=%s\n", i, att.Name, att.Kind)
		if err := c.sendAttachment(ctx, msg.ChatID, att, msg); err != nil {
			log.Printf("feishu: failed to send attachment: %v\n", err)
			return err
		}
	}

	return nil
}

func (c *Channel) sendTextMessage(ctx context.Context, chatID, text string, msg bus.OutboundMessage) error {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return err
	}

	// Build message content - text message format
	content := map[string]string{"text": text}
	contentJSON, _ := json.Marshal(content)

	// Determine receiver type and ID
	receiverType, receiveID := c.resolveReceiverType(chatID)

	// Build request body
	body := map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(contentJSON),
	}

	// Add reply_id if present (use message_id for reply)
	replyTo := c.resolveReplyTo(msg)
	if replyTo != "" {
		body["reply_id"] = replyTo
	}

	reqBody, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=%s", feishuAPIBase, receiverType)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("feishu: sending message to %s (%s): %s\n", receiveID, receiverType, text)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("feishu: send response: status=%d, body=%s\n", resp.StatusCode, string(respBody))

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return err
	}

	if result.Code != 0 {
		return fmt.Errorf("feishu API error: code=%d, msg=%s", result.Code, result.Msg)
	}

	log.Printf("feishu: message sent successfully, message_id=%s\n", result.Data.MessageID)
	return nil
}

func (c *Channel) sendAttachment(ctx context.Context, chatID string, att bus.Attachment, msg bus.OutboundMessage) error {
	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return err
	}

	var fileData []byte
	var fileName string

	if att.Data != nil {
		fileData = att.Data
		fileName = att.Name
	} else if att.LocalPath != "" {
		data, err := os.ReadFile(att.LocalPath)
		if err != nil {
			return err
		}
		fileData = data
		fileName = att.Name
	} else {
		return fmt.Errorf("attachment has no data or local path")
	}

	fileID, err := c.uploadFile(ctx, token, fileData, fileName, att.MIMEType)
	if err != nil {
		return err
	}

	msgType := "image"
	contentKey := "image_key"
	if strings.HasPrefix(att.MIMEType, "video/") {
		msgType = "video"
		contentKey = "video_key"
	} else if !strings.HasPrefix(att.MIMEType, "image/") {
		msgType = "file"
		contentKey = "file_key"
	}

	_, receiveID := c.resolveReceiverType(chatID)

	content := map[string]string{contentKey: fileID}
	contentJSON, _ := json.Marshal(content)

	body := map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    string(contentJSON),
	}

	replyTo := c.resolveReplyTo(msg)
	if replyTo != "" {
		body["reply_id"] = replyTo
	}

	reqBody, _ := json.Marshal(body)
	receiverType, _ := c.resolveReceiverType(chatID)
	url := fmt.Sprintf("%s/open-apis/im/v1/messages?receive_id_type=%s", feishuAPIBase, receiverType)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("feishu: sending %s: key=%s\n", msgType, fileID)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("feishu: send response: status=%d, body=%s\n", resp.StatusCode, string(respBody))

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data *struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return err
	}

	if result.Code != 0 {
		return fmt.Errorf("feishu API error: code=%d, msg=%s", result.Code, result.Msg)
	}

	log.Printf("feishu: %s sent successfully, message_id=%s\n", msgType, result.Data.MessageID)

	return nil
}

func (c *Channel) uploadFile(ctx context.Context, token string, data []byte, filename, mimeType string) (string, error) {
	// Determine file type and use appropriate API
	isImage := strings.HasPrefix(mimeType, "image/")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Create form-data for file/image
	fieldName := "file"
	url := fmt.Sprintf("%s/open-apis/im/v1/files", feishuAPIBase)

	if isImage {
		// Use image API for images
		fieldName = "image"
		url = fmt.Sprintf("%s/open-apis/im/v1/images", feishuAPIBase)
	}

	filePart, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		return "", err
	}
	if _, err := filePart.Write(data); err != nil {
		return "", err
	}

	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	log.Printf("feishu: uploading %s (name=%s, size=%d) to %s\n", fieldName, filename, len(data), url)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("feishu: upload response: status=%d, body=%s\n", resp.StatusCode, string(respBody))

	if isImage {
		// Parse image response
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				ImageKey string `json:"image_key"`
			} `json:"data"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", err
		}

		if result.Code != 0 {
			return "", fmt.Errorf("feishu upload error: code=%d, msg=%s", result.Code, result.Msg)
		}

		if result.Data.ImageKey == "" {
			return "", fmt.Errorf("feishu upload succeeded but no image_key returned")
		}
		log.Printf("feishu: image upload succeeded, image_key=%s\n", result.Data.ImageKey)
		return result.Data.ImageKey, nil
	} else {
		// Parse file response
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				FileKey   string `json:"file_key"`
				FileToken string `json:"file_token"`
			} `json:"data"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", err
		}

		if result.Code != 0 {
			return "", fmt.Errorf("feishu upload error: code=%d, msg=%s", result.Code, result.Msg)
		}

		key := result.Data.FileKey
		if key == "" {
			key = result.Data.FileToken
		}
		if key == "" {
			return "", fmt.Errorf("feishu upload succeeded but no file_key returned")
		}
		log.Printf("feishu: file upload succeeded, file_key=%s\n", key)
		return key, nil
	}
}

func (c *Channel) getTenantAccessToken(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/open-apis/auth/v3/tenant_access_token/internal", feishuAPIBase)
	body := map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	}
	reqBody, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Code != 0 {
		return "", fmt.Errorf("feishu auth error: code=%d, msg=%s", result.Code, result.Msg)
	}

	return result.TenantAccessToken, nil
}

func (c *Channel) resolveReceiverType(chatID string) (receiverType, receiveID string) {
	chatID = strings.TrimSpace(chatID)

	if strings.HasPrefix(chatID, "ou_") {
		return "open_id", chatID
	}
	if strings.HasPrefix(chatID, "u_") {
		return "user_id", chatID
	}
	if strings.HasPrefix(chatID, "on_") {
		return "union_id", chatID
	}
	return "chat_id", chatID
}

func (c *Channel) resolveReplyTo(msg bus.OutboundMessage) string {
	if strings.TrimSpace(msg.Delivery.ReplyToID) != "" {
		return msg.Delivery.ReplyToID
	}
	if strings.TrimSpace(msg.ReplyTo) != "" {
		return msg.ReplyTo
	}
	if strings.TrimSpace(msg.Delivery.ThreadID) != "" {
		return msg.Delivery.ThreadID
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func randInt(min, max int) int {
	return min + int(time.Now().UnixNano()%int64(max-min))
}
