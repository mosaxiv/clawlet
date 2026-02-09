package bus

import (
	"context"
)

type InboundMessage struct {
	Channel    string
	SenderID   string
	ChatID     string
	Content    string
	SessionKey string // usually "channel:chat_id"
	// Media/metadata can be added later if needed (keep minimal for now).
}

type OutboundMessage struct {
	Channel  string
	ChatID   string
	Content  string
	ReplyTo  string
	Metadata map[string]any
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
