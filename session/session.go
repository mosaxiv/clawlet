package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Timestamp string   `json:"timestamp,omitempty"`
	ToolsUsed []string `json:"tools_used,omitempty"`
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

	mu      sync.Mutex
	version uint64
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
	s.AddWithTools(role, content, nil)
}

func (s *Session) AddWithTools(role, content string, toolsUsed []string) {
	var copied []string
	if len(toolsUsed) > 0 {
		copied = make([]string, 0, len(toolsUsed))
		for _, name := range toolsUsed {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			copied = append(copied, name)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now().Format(time.RFC3339Nano),
		ToolsUsed: copied,
	})
	s.UpdatedAt = time.Now()
	s.version++
}

func (s *Session) History(max int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.Messages
	if max > 0 && len(msgs) > max {
		msgs = msgs[len(msgs)-max:]
	}
	return cloneMessages(msgs)
}

func (s *Session) NeedsConsolidation(memoryWindow int) bool {
	if memoryWindow <= 0 {
		memoryWindow = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Messages) > memoryWindow
}

func (s *Session) SnapshotForConsolidation(memoryWindow int) (oldMessages []Message, keep int, version uint64, ok bool) {
	if memoryWindow <= 0 {
		memoryWindow = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.Messages)
	if n <= memoryWindow {
		return nil, 0, 0, false
	}
	keep = min(10, max(2, memoryWindow/2))
	if keep >= n {
		return nil, 0, 0, false
	}
	oldMessages = cloneMessages(s.Messages[:n-keep])
	return oldMessages, keep, s.version, true
}

func (s *Session) ApplyConsolidation(version uint64, keep int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.version != version {
		return false
	}
	if keep < 0 || keep >= len(s.Messages) {
		return false
	}
	s.Messages = cloneMessages(s.Messages[len(s.Messages)-keep:])
	s.UpdatedAt = time.Now()
	s.version++
	return true
}

func Save(dir string, s *Session) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, safeFilename(strings.ReplaceAll(s.Key, ":", "_"))+".jsonl")

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
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
	return nil
}

func cloneMessages(in []Message) []Message {
	out := make([]Message, 0, len(in))
	for _, m := range in {
		msg := Message{
			Role:      m.Role,
			Content:   m.Content,
			Timestamp: m.Timestamp,
		}
		if len(m.ToolsUsed) > 0 {
			msg.ToolsUsed = append([]string{}, m.ToolsUsed...)
		}
		out = append(out, msg)
	}
	return out
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
