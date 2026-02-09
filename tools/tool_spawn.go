package tools

import (
	"context"
	"errors"
	"strings"
)

func (r *Registry) spawn(ctx context.Context, task, label, originChannel, originChatID string) (string, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return "", errors.New("task is empty")
	}
	if r.Spawn == nil {
		return "", errors.New("spawn not configured")
	}
	id, err := r.Spawn(ctx, task, strings.TrimSpace(label), originChannel, originChatID)
	if err != nil {
		return "", err
	}
	return id, nil
}
