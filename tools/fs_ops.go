package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mosaxiv/clawlet/paths"
)

var sensitiveDirNames = []string{
	"auth",
	"whatsapp-auth",
}

func hasParentTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func isSameOrChildPath(path string, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func blockedSensitivePaths() []string {
	cfgDir, err := paths.ConfigDir()
	if err != nil || strings.TrimSpace(cfgDir) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || strings.TrimSpace(home) == "" {
			return nil
		}
		cfgDir = filepath.Join(home, ".clawlet")
	}
	out := make([]string, 0, len(sensitiveDirNames))
	for _, dirName := range sensitiveDirNames {
		out = append(out, filepath.Clean(filepath.Join(cfgDir, dirName)))
	}
	return out
}

func ensurePathAllowedByPolicy(abs string) error {
	abs = filepath.Clean(abs)
	if abs == string(filepath.Separator) {
		return errors.New("path is blocked by safety policy: /")
	}
	for _, blocked := range blockedSensitivePaths() {
		if isSameOrChildPath(abs, blocked) {
			return fmt.Errorf("path is blocked by safety policy: %s", abs)
		}
	}
	return nil
}

func (r *Registry) workspaceAbs() (string, error) {
	wsAbs, err := filepath.Abs(r.WorkspaceDir)
	if err != nil {
		return "", err
	}
	wsAbs = filepath.Clean(wsAbs)
	if wsAbs == string(filepath.Separator) {
		return "", errors.New("workspace root '/' is not allowed when tools are restricted")
	}
	return wsAbs, nil
}

func (r *Registry) resolvePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is empty")
	}
	if strings.ContainsRune(p, '\x00') {
		return "", errors.New("path contains null byte")
	}
	if hasParentTraversal(p) {
		return "", errors.New("path traversal is not allowed")
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "..%2f") || strings.Contains(lower, "%2f..") || strings.Contains(lower, "%2e%2e") {
		return "", errors.New("encoded path traversal is not allowed")
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
	if err := ensurePathAllowedByPolicy(abs); err != nil {
		return "", err
	}

	if !r.RestrictToWorkspace {
		return abs, nil
	}

	wsAbs, err := r.workspaceAbs()
	if err != nil {
		return "", err
	}
	if abs == wsAbs {
		return abs, nil
	}
	if !isSameOrChildPath(abs, wsAbs) {
		return "", fmt.Errorf("path is outside workspace: %s", abs)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return abs, nil
		}
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if err := ensurePathAllowedByPolicy(resolved); err != nil {
		return "", err
	}
	if !isSameOrChildPath(resolved, wsAbs) {
		return "", fmt.Errorf("path is outside workspace: %s", resolved)
	}
	return resolved, nil
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
	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	parentResolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if r.RestrictToWorkspace {
		wsAbs, err := r.workspaceAbs()
		if err != nil {
			return "", err
		}
		if !isSameOrChildPath(parentResolved, wsAbs) {
			return "", fmt.Errorf("path is outside workspace: %s", parentResolved)
		}
	}
	target := filepath.Join(parentResolved, filepath.Base(abs))
	if err := ensurePathAllowedByPolicy(target); err != nil {
		return "", err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing to write through symlink: %s", target)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), target), nil
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
