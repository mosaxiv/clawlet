package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mosaxiv/clawlet/paths"
)

// DailyTokenUsage tracks token usage per calendar day.
type DailyTokenUsage struct {
	Calls            int `json:"calls"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// TokenUsageStore is a persistent, thread-safe token usage tracker.
type TokenUsageStore struct {
	mu   sync.Mutex
	path string
	data map[string]*DailyTokenUsage
}

// DefaultTokenUsageStore returns the singleton store used by the llm package.
func DefaultTokenUsageStore() *TokenUsageStore {
	return globalTokenUsage
}

// NewTokenUsageStore creates a usage store backed by the given file path.
// The file will be loaded if it exists.
func NewTokenUsageStore(path string) (*TokenUsageStore, error) {
	store := &TokenUsageStore{
		path: path,
		data: map[string]*DailyTokenUsage{},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *TokenUsageStore) load() error {
	if s.path == "" {
		return fmt.Errorf("token usage path is empty")
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var raw map[string]DailyTokenUsage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		copy := v
		s.data[k] = &copy
	}
	return nil
}

func (s *TokenUsageStore) save() error {
	if s.path == "" {
		return fmt.Errorf("token usage path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	out := map[string]DailyTokenUsage{}
	for k, v := range s.data {
		out[k] = *v
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}

func (s *TokenUsageStore) Add(prompt, completion, total int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string]*DailyTokenUsage{}
	}
	key := time.Now().UTC().Format("2006-01-02")
	d, ok := s.data[key]
	if !ok {
		d = &DailyTokenUsage{}
		s.data[key] = d
	}
	d.Calls++
	d.PromptTokens += prompt
	d.CompletionTokens += completion
	d.TotalTokens += total
	return s.save()
}

func (s *TokenUsageStore) Get(day string) (DailyTokenUsage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		return DailyTokenUsage{}, false
	}
	d, ok := s.data[day]
	if !ok {
		return DailyTokenUsage{}, false
	}
	return *d, true
}

func (s *TokenUsageStore) All() map[string]DailyTokenUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]DailyTokenUsage{}
	for k, v := range s.data {
		out[k] = *v
	}
	return out
}

var globalTokenUsage *TokenUsageStore

func init() {
	path := paths.TokenUsagePath()
	store, err := NewTokenUsageStore(path)
	if err != nil {
		// Fail silently; token usage is non-critical.
		store = &TokenUsageStore{path: path, data: map[string]*DailyTokenUsage{}}
	}
	globalTokenUsage = store
}

func RecordTokenUsage(prompt, completion, total int) {
	if globalTokenUsage == nil {
		return
	}
	_ = globalTokenUsage.Add(prompt, completion, total)
}

func RecordTokenUsageFromResponse(body []byte) {
	prompt, completion, total := parseTokenUsage(body)
	if prompt == 0 && completion == 0 && total == 0 {
		return
	}
	RecordTokenUsage(prompt, completion, total)
}

func parseTokenUsage(body []byte) (prompt, completion, total int) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}

	getInt := func(v any) int {
		switch t := v.(type) {
		case float64:
			return int(t)
		case int:
			return t
		case int64:
			return int(t)
		case json.Number:
			i, _ := t.Int64()
			return int(i)
		default:
			return 0
		}
	}

	// OpenAI compatible
	if u, ok := m["usage"].(map[string]any); ok {
		prompt = getInt(u["prompt_tokens"])
		completion = getInt(u["completion_tokens"])
		total = getInt(u["total_tokens"])
		if total == 0 {
			total = prompt + completion
		}
	}

	// Gemini tokenUsage
	if total == 0 {
		if u, ok := m["tokenUsage"].(map[string]any); ok {
			prompt = getInt(u["promptTokens"])
			completion = getInt(u["completionTokens"])
			total = getInt(u["totalTokens"])
			if total == 0 {
				total = prompt + completion
			}
		}
	}

	// Anthropic / other providers might expose prompt/completion tokens at top level.
	if prompt == 0 {
		prompt = getInt(m["prompt_tokens"])
	}
	if completion == 0 {
		completion = getInt(m["completion_tokens"])
	}
	if total == 0 {
		total = getInt(m["total_tokens"])
	}

	if total == 0 {
		total = prompt + completion
	}
	return
}
