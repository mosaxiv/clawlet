package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

type metadataLine struct {
	Type      string         `json:"_type"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
	Metadata  map[string]any `json:"metadata"`
}

type Session struct {
	Key       string
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message
	Metadata  map[string]any
}

type Manager struct {
	Dir   string
	cache map[string]*Session
	mu    sync.Mutex
}

func NewManager(dir string) *Manager {
	return &Manager{Dir: dir, cache: map[string]*Session{}}
}

func (m *Manager) GetOrCreate(key string) (*Session, error) {
	m.mu.Lock()
	if s, ok := m.cache[key]; ok {
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()
	s, err := Load(m.Dir, key)
	if err != nil {
		return nil, err
	}
	if s == nil {
		s = New(key)
	}
	m.mu.Lock()
	m.cache[key] = s
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) Save(s *Session) error {
	if err := Save(m.Dir, s); err != nil {
		return err
	}
	m.mu.Lock()
	m.cache[s.Key] = s
	m.mu.Unlock()
	return nil
}

func Load(dir, key string) (*Session, error) {
	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(key, ":", "_"))+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	s := &Session{
		Key:      key,
		Messages: []Message{},
		Metadata: map[string]any{},
	}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		if raw["_type"] == "metadata" {
			var ml metadataLine
			if err := json.Unmarshal([]byte(line), &ml); err == nil {
				if t, err := time.Parse(time.RFC3339Nano, ml.CreatedAt); err == nil {
					s.CreatedAt = t
				}
				if t, err := time.Parse(time.RFC3339Nano, ml.UpdatedAt); err == nil {
					s.UpdatedAt = t
				}
				if ml.Metadata != nil {
					s.Metadata = ml.Metadata
				}
			}
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			s.Messages = append(s.Messages, m)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now()
	}
	return s, nil
}

func New(key string) *Session {
	now := time.Now()
	return &Session{
		Key:       key,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []Message{},
		Metadata:  map[string]any{},
	}
}

func (s *Session) Add(role, content string) {
	s.Messages = append(s.Messages, Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
	s.UpdatedAt = time.Now()
}

func (s *Session) History(max int) []Message {
	msgs := s.Messages
	if max > 0 && len(msgs) > max {
		msgs = msgs[len(msgs)-max:]
	}
	out := make([]Message, 0, len(msgs))
	out = append(out, msgs...)
	return out
}

func Save(dir string, s *Session) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(s.Key, ":", "_"))+".jsonl")
	tmp := path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)

	meta := metadataLine{
		Type:      "metadata",
		CreatedAt: s.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: s.UpdatedAt.Format(time.RFC3339Nano),
		Metadata:  s.Metadata,
	}
	if b, err := json.Marshal(meta); err == nil {
		if _, err := bw.Write(append(b, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	for _, m := range s.Messages {
		if b, err := json.Marshal(m); err == nil {
			if _, err := bw.Write(append(b, '\n')); err != nil {
				_ = f.Close()
				return err
			}
		}
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

var safeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeFilename(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	s = safeRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "default"
	}
	return s
}
