package memory

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	Workspace string
	Dir       string
	LongTerm  string
}

func New(workspace string) *Store {
	dir := filepath.Join(workspace, "memory")
	return &Store{
		Workspace: workspace,
		Dir:       dir,
		LongTerm:  filepath.Join(dir, "MEMORY.md"),
	}
}

func TodayDate() string {
	return time.Now().Format("2006-01-02")
}

func (s *Store) TodayPath() string {
	return filepath.Join(s.Dir, TodayDate()+".md")
}

func (s *Store) EnsureInitialized() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(s.LongTerm); err != nil {
		_ = os.WriteFile(s.LongTerm, []byte("# Long-term Memory\n\n"), 0o644)
	}
	return nil
}

func (s *Store) ReadLongTerm() string {
	_ = s.EnsureInitialized()
	b, err := os.ReadFile(s.LongTerm)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Store) ReadToday() string {
	_ = s.EnsureInitialized()
	p := s.TodayPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Store) GetContext() string {
	longTerm := strings.TrimSpace(s.ReadLongTerm())
	today := strings.TrimSpace(s.ReadToday())

	var parts []string
	if longTerm != "" {
		parts = append(parts, "## Long-term Memory\n"+truncate(longTerm, 64<<10))
	}
	if today != "" {
		parts = append(parts, "## Today's Notes\n"+truncate(today, 64<<10))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n\n(truncated)"
}
