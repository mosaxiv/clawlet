package feishu

import (
	"context"
	"testing"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/config"
)

func TestFeishuChannelName(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "test_secret",
	}
	ch := New(cfg, b)

	if ch.Name() != "feishu" {
		t.Errorf("expected name 'feishu', got %s", ch.Name())
	}
}

func TestFeishuChannelNotRunningInitially(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "test_secret",
	}
	ch := New(cfg, b)

	if ch.IsRunning() {
		t.Error("expected channel to not be running initially")
	}
}

func TestFeishuChannelRequiresAppID(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "",
		AppSecret: "test_secret",
	}
	ch := New(cfg, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := ch.Start(ctx)
	if err == nil {
		t.Error("expected error for empty AppID")
	}
}

func TestFeishuChannelRequiresAppSecret(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "",
	}
	ch := New(cfg, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := ch.Start(ctx)
	if err == nil {
		t.Error("expected error for empty AppSecret")
	}
}

func TestFeishuStripBotMention(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "test_secret",
	}
	ch := New(cfg, b)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no mention",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "mention at start",
			input:    `<at user_id="123">bot</at> hello`,
			expected: "hello",
		},
		{
			name:     "mention with colon",
			input:    `<at user_id="123">bot</at>: hello`,
			expected: "hello",
		},
		{
			name:     "mention with comma",
			input:    `<at user_id="123">bot</at>, hello`,
			expected: "hello",
		},
		{
			name:     "chinese punctuation",
			input:    `<at user_id="123">bot</at>:你好`,
			expected: "你好",
		},
		{
			name:     "multiple mentions",
			input:    `<at user_id="123">bot</at><at user_id="456">user</at> hello`,
			expected: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ch.stripBotMention(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestFeishuResolveReceiverType(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "test_secret",
	}
	ch := New(cfg, b)

	tests := []struct {
		name          string
		chatID        string
		expectedType  string
		expectedID    string
	}{
		{
			name:         "open_id",
			chatID:       "ou_xxxxx",
			expectedType: "open_id",
			expectedID:   "ou_xxxxx",
		},
		{
			name:         "user_id",
			chatID:       "u_xxxxx",
			expectedType: "user_id",
			expectedID:   "u_xxxxx",
		},
		{
			name:         "union_id",
			chatID:       "on_xxxxx",
			expectedType: "union_id",
			expectedID:   "on_xxxxx",
		},
		{
			name:         "chat_id p2p",
			chatID:       "oc_xxxxx",
			expectedType: "chat_id",
			expectedID:   "oc_xxxxx",
		},
		{
			name:         "chat_id group",
			chatID:       "ch_xxxxx",
			expectedType: "chat_id",
			expectedID:   "ch_xxxxx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiverType, receiveID := ch.resolveReceiverType(tt.chatID)
			if receiverType != tt.expectedType {
				t.Errorf("expected receiver type %s, got %s", tt.expectedType, receiverType)
			}
			if receiveID != tt.expectedID {
				t.Errorf("expected receiver ID %s, got %s", tt.expectedID, receiveID)
			}
		})
	}
}

func TestFeishuResolveReplyTo(t *testing.T) {
	b := bus.New(8)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "test_secret",
	}
	ch := New(cfg, b)

	tests := []struct {
		name         string
		msg          bus.OutboundMessage
		expectedReply string
	}{
		{
			name: "from Delivery.ReplyToID",
			msg: bus.OutboundMessage{
				Delivery: bus.Delivery{
					ReplyToID: "msg123",
				},
			},
			expectedReply: "msg123",
		},
		{
			name: "from ReplyTo field",
			msg: bus.OutboundMessage{
				ReplyTo: "msg456",
			},
			expectedReply: "msg456",
		},
		{
			name: "from Delivery.ThreadID",
			msg: bus.OutboundMessage{
				Delivery: bus.Delivery{
					ThreadID: "thread789",
				},
			},
			expectedReply: "thread789",
		},
		{
			name: "empty",
			msg:  bus.OutboundMessage{},
			expectedReply: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ch.resolveReplyTo(tt.msg)
			if result != tt.expectedReply {
				t.Errorf("expected %s, got %s", tt.expectedReply, result)
			}
		})
	}
}

func TestFeishuAllowList(t *testing.T) {
	b := bus.New(8)

	// Test with empty allowFrom (allow all)
	cfg := config.FeishuConfig{
		Enabled:   true,
		AppID:     "cli_test",
		AppSecret: "test_secret",
		AllowFrom: []string{},
	}
	ch := New(cfg, b)

	ctx := context.Background()

	// Create a test message
	msg := &feishuEventMessage{
		MessageID:  strPtr("msg123"),
		ChatID:     strPtr("oc_test"),
		SenderID:   &feishuUserID{OpenId: strPtr("ou_sender")},
		MsgType:    strPtr("text"),
		Content:    strPtr(`{"text":"hello"}`),
		ChatType:   strPtr("p2p"),
	}

	// Should not panic with empty allowFrom
	ch.processMessageV2(ctx, msg, []byte{}, nil, "ou_sender")
}

func strPtr(s string) *string {
	return &s
}
