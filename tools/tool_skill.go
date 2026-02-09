package tools

import (
	"errors"
	"fmt"
	"strings"
)

func (r *Registry) readSkill(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is empty")
	}
	if r.ReadSkill == nil {
		return "", errors.New("skills not configured")
	}
	if s, ok := r.ReadSkill(name); ok {
		return s, nil
	}
	return "", fmt.Errorf("skill not found: %s", name)
}
