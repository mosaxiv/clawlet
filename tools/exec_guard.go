package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var execDenyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\s+-[a-z]*r[a-z]*f?[a-z]*\b`), // rm -r, rm -rf, rm -fr
	regexp.MustCompile(`\bdel\s+/[fq]\b`),                 // del /f, del /q (Windows)
	regexp.MustCompile(`\brmdir\s+/s\b`),                  // rmdir /s (Windows)
	regexp.MustCompile(`\b(format|mkfs|diskpart)\b`),      // disk operations
	regexp.MustCompile(`\bdd\s+if=`),                      // dd
	regexp.MustCompile(`>\s*/dev/sd`),                     // write to disk
	regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),  // system power
	regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),             // fork bomb
}

var (
	reHomeToken = regexp.MustCompile(`(^|\s)~(/|\s|$)`)
	// Absolute POSIX paths only: require start of token, not "./foo/bar" etc.
	rePosixAbs = regexp.MustCompile(`(^|[\s"'(=,:><])(/[^ \t\r\n"'` + "`" + `]*)`)
	reHomeAbs  = regexp.MustCompile(`~\/[^ \t\r\n"'` + "`" + `]+`)
	reWinAbs   = regexp.MustCompile(`[A-Za-z]:\\[^\\\"'\s]+`)
)

func containsSingleAmpersand(s string) bool {
	b := []byte(s)
	for i := range b {
		if b[i] != '&' {
			continue
		}
		prev := i > 0 && b[i-1] == '&'
		next := i+1 < len(b) && b[i+1] == '&'
		if !prev && !next {
			return true
		}
	}
	return false
}

func hasToken(command string, token string) bool {
	for _, field := range strings.Fields(command) {
		if field == token || strings.HasSuffix(field, "/"+token) {
			return true
		}
	}
	return false
}

func expandHomePath(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/"))
}

func guardExecCommand(command string, workspaceDir string, restrict bool) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(cmd, "`") ||
		strings.Contains(cmd, "$(") ||
		strings.Contains(cmd, "${") ||
		strings.Contains(cmd, "<(") ||
		strings.Contains(cmd, ">(") {
		return "Error: Command blocked by safety guard (unsafe shell expansion detected)"
	}
	if strings.Contains(cmd, ";") || strings.Contains(cmd, "\n") {
		return "Error: Command blocked by safety guard (command chaining detected)"
	}
	if strings.Contains(cmd, ">") {
		return "Error: Command blocked by safety guard (redirection is not allowed)"
	}
	if containsSingleAmpersand(cmd) {
		return "Error: Command blocked by safety guard (background chaining detected)"
	}
	if hasToken(cmd, "tee") {
		return "Error: Command blocked by safety guard (tee is not allowed)"
	}

	for _, re := range execDenyPatterns {
		if re.MatchString(lower) {
			return "Error: Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	var wsAbs string
	var isWithin func(p string) bool
	if restrict {
		if strings.Contains(cmd, "../") || strings.Contains(cmd, `..\`) {
			return "Error: Command blocked by safety guard (path traversal detected)"
		}
		if reHomeToken.MatchString(cmd) {
			return "Error: Command blocked by safety guard (path outside workspace)"
		}

		wsAbsResolved, err := filepath.Abs(workspaceDir)
		if err != nil {
			wsAbsResolved = filepath.Clean(workspaceDir)
		}
		wsAbs = filepath.Clean(wsAbsResolved)
		isWithin = func(p string) bool {
			p = filepath.Clean(p)
			if p == wsAbs {
				return true
			}
			prefix := wsAbs + string(filepath.Separator)
			return strings.HasPrefix(p, prefix)
		}
	}

	winPaths := reWinAbs.FindAllString(cmd, -1)
	posixMatches := rePosixAbs.FindAllStringSubmatch(cmd, -1)
	posixPaths := make([]string, 0, len(posixMatches))
	for _, m := range posixMatches {
		if len(m) >= 3 {
			posixPaths = append(posixPaths, m[2])
		}
	}
	homePaths := reHomeAbs.FindAllString(cmd, -1)

	for _, raw := range append(append(winPaths, posixPaths...), homePaths...) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		raw = expandHomePath(raw)
		if err := ensurePathAllowedByPolicy(raw); err != nil {
			return "Error: Command blocked by safety guard (sensitive path is not allowed)"
		}
		if !restrict {
			continue
		}
		if isWithin(raw) {
			continue
		}
		return "Error: Command blocked by safety guard (path outside workspace)"
	}

	return ""
}
