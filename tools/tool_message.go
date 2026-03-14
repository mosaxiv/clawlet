package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/bus"
)

func (r *Registry) message(ctx context.Context, channel, chatID, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("content is empty")
	}
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(chatID) == "" {
		return "", errors.New("no target channel/chat_id")
	}
	if r.Outbound == nil {
		return "", errors.New("message sending not configured")
	}
	msg := bus.OutboundMessage{Channel: channel, ChatID: chatID, Content: content}
	if err := r.Outbound(ctx, msg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Message sent to %s:%s", channel, chatID), nil
}

func (r *Registry) sendFile(ctx context.Context, filePath, fileName, channel, chatID, caption string) (string, error) {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return "", errors.New("file_path is empty")
	}
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(chatID) == "" {
		return "", errors.New("no target channel/chat_id")
	}
	if r.Outbound == nil {
		return "", errors.New("file sending not configured")
	}

	// Resolve file path relative to workspace
	if !strings.HasPrefix(filePath, "/") {
		filePath = strings.TrimPrefix(filePath, "./")
		if r.WorkspaceDir != "" {
			filePath = fmt.Sprintf("%s/%s", r.WorkspaceDir, filePath)
		}
	}

	// If fileName is not provided, use the base name of the file
	if fileName == "" {
		parts := strings.Split(filePath, "/")
		fileName = parts[len(parts)-1]
	}

	// Determine file kind based on extension or MIME type
	kind := "file"
	if strings.HasSuffix(strings.ToLower(fileName), ".jpg") || strings.HasSuffix(strings.ToLower(fileName), ".jpeg") || strings.HasSuffix(strings.ToLower(fileName), ".png") || strings.HasSuffix(strings.ToLower(fileName), ".gif") {
		kind = "image"
	} else if strings.HasSuffix(strings.ToLower(fileName), ".mp4") || strings.HasSuffix(strings.ToLower(fileName), ".avi") || strings.HasSuffix(strings.ToLower(fileName), ".mov") {
		kind = "video"
	} else if strings.HasSuffix(strings.ToLower(fileName), ".mp3") || strings.HasSuffix(strings.ToLower(fileName), ".wav") || strings.HasSuffix(strings.ToLower(fileName), ".ogg") {
		kind = "audio"
	}

	attachment := bus.Attachment{
		Name:      fileName,
		Kind:      kind,
		LocalPath: filePath,
	}

	msg := bus.OutboundMessage{
		Channel:     channel,
		ChatID:      chatID,
		Content:     strings.TrimSpace(caption),
		Attachments: []bus.Attachment{attachment},
	}

	if err := r.Outbound(ctx, msg); err != nil {
		return "", err
	}
	return fmt.Sprintf("File '%s' sent to %s:%s", fileName, channel, chatID), nil
}
