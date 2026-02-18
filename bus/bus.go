package bus

import (
	"context"
	"strings"
)

type Delivery struct {
	MessageID string
	ReplyToID string
	ThreadID  string
	IsDirect  bool
}

type Attachment struct {
	ID        string
	Name      string
	MIMEType  string
	Kind      string
	SizeBytes int64
	URL       string
	LocalPath string
	Data      []byte
	Headers   map[string]string
}

func InferAttachmentKind(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	default:
		return "file"
	}
}

type InboundMessage struct {
	Channel     string
	SenderID    string
	ChatID      string
	Content     string
	Attachments []Attachment
	SessionKey  string // usually "channel:chat_id"
	Delivery    Delivery
}

type OutboundMessage struct {
	Channel  string
	ChatID   string
	Content  string
	ReplyTo  string
	Delivery Delivery
}

type Bus struct {
	in  chan InboundMessage
	out chan OutboundMessage
}

func New(buffer int) *Bus {
	if buffer <= 0 {
		buffer = 64
	}
	return &Bus{
		in:  make(chan InboundMessage, buffer),
		out: make(chan OutboundMessage, buffer),
	}
}

func (b *Bus) PublishInbound(ctx context.Context, msg InboundMessage) error {
	select {
	case b.in <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bus) PublishOutbound(ctx context.Context, msg OutboundMessage) error {
	select {
	case b.out <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bus) ConsumeInbound(ctx context.Context) (InboundMessage, error) {
	select {
	case msg := <-b.in:
		return msg, nil
	case <-ctx.Done():
		return InboundMessage{}, ctx.Err()
	}
}

func (b *Bus) ConsumeOutbound(ctx context.Context) (OutboundMessage, error) {
	select {
	case msg := <-b.out:
		return msg, nil
	case <-ctx.Done():
		return OutboundMessage{}, ctx.Err()
	}
}
