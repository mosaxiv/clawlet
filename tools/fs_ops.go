package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (r *Registry) resolvePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is empty")
	}
	// Expand "~/".
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
			}
		}
	}

	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(r.WorkspaceDir, p)
		abs = filepath.Clean(abs)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}

	if !r.RestrictToWorkspace {
		return abs, nil
	}

	wsAbs, err := filepath.Abs(r.WorkspaceDir)
	if err != nil {
		return "", err
	}
	wsAbs = filepath.Clean(wsAbs)
	if abs == wsAbs {
		return abs, nil
	}
	prefix := wsAbs + string(filepath.Separator)
	if !strings.HasPrefix(abs, prefix) {
		return "", fmt.Errorf("path is outside workspace: %s", abs)
	}
	return abs, nil
}

func (r *Registry) readFile(path string) (string, error) {
	abs, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	const max = 512 << 10
	if len(b) > max {
		b = b[:max]
		return string(b) + "\n\n(truncated)", nil
	}
	return string(b), nil
}

func (r *Registry) writeFile(path, content string) (string, error) {
	abs, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), abs), nil
}

func (r *Registry) editFile(path string, startLine, endLine int, newText string) (string, error) {
	abs, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	s := string(b)
	// Preserve trailing newline behavior: split on "\n" and keep last empty element if present.
	lines := strings.Split(s, "\n")

	if startLine <= 0 {
		return "", fmt.Errorf("startLine must be >= 1")
	}
	if endLine < 0 {
		return "", fmt.Errorf("endLine must be >= 0")
	}
	if startLine > len(lines)+1 {
		return "", fmt.Errorf("startLine out of range: %d (max %d)", startLine, len(lines)+1)
	}
	if endLine > len(lines) {
		return "", fmt.Errorf("endLine out of range: %d (max %d)", endLine, len(lines))
	}

	var out []string
	if endLine < startLine {
		// Insert before startLine
		i := startLine - 1
		out = append(out, lines[:i]...)
		out = append(out, strings.Split(newText, "\n")...)
		out = append(out, lines[i:]...)
	} else {
		// Replace inclusive
		i := startLine - 1
		j := endLine
		out = append(out, lines[:i]...)
		out = append(out, strings.Split(newText, "\n")...)
		out = append(out, lines[j:]...)
	}

	newContent := strings.Join(out, "\n")
	if err := os.WriteFile(abs, []byte(newContent), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", abs), nil
}

func (r *Registry) editFileReplace(path, oldText, newText string) (string, error) {
	abs, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(oldText) == "" {
		return "", errors.New("old_text is empty")
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	content := string(b)
	if !strings.Contains(content, oldText) {
		return "", errors.New("old_text not found in file")
	}
	count := strings.Count(content, oldText)
	if count > 1 {
		return "", fmt.Errorf("old_text appears %d times; make it unique", count)
	}
	updated := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", abs), nil
}

func (r *Registry) listDir(path string, recursive bool, maxEntries int) (string, error) {
	if maxEntries <= 0 {
		maxEntries = 200
	}
	abs, err := r.resolvePath(path)
	if err != nil {
		return "", err
	}
	var entries []string
	add := func(p string) bool {
		entries = append(entries, p)
		return len(entries) < maxEntries
	}

	if !recursive {
		d, err := os.ReadDir(abs)
		if err != nil {
			return "", err
		}
		for _, e := range d {
			if !add(e.Name()) {
				break
			}
		}
	} else {
		err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if p == abs {
				return nil
			}
			rel, _ := filepath.Rel(abs, p)
			if d.IsDir() {
				rel += string(filepath.Separator)
			}
			if !add(rel) {
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}

	b, _ := json.Marshal(entries)
	return string(b), nil
}
