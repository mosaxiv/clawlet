package tools

import (
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
	rePosixAbs = regexp.MustCompile(`(^|[\s"'(=,:><])(/[^ \t\r\n"'` + "`" + `]+)`)
	reWinAbs   = regexp.MustCompile(`[A-Za-z]:\\[^\\\"'\s]+`)
)

func guardExecCommand(command string, workspaceDir string, restrict bool) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return ""
	}
	lower := strings.ToLower(cmd)

	for _, re := range execDenyPatterns {
		if re.MatchString(lower) {
			return "Error: Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	if !restrict {
		return ""
	}

	if strings.Contains(cmd, "../") || strings.Contains(cmd, `..\`) {
		return "Error: Command blocked by safety guard (path traversal detected)"
	}

	if reHomeToken.MatchString(cmd) {
		return "Error: Command blocked by safety guard (path outside workspace)"
	}

	wsAbs, err := filepath.Abs(workspaceDir)
	if err != nil {
		wsAbs = filepath.Clean(workspaceDir)
	}
	wsAbs = filepath.Clean(wsAbs)

	isWithin := func(p string) bool {
		p = filepath.Clean(p)
		if p == wsAbs {
			return true
		}
		prefix := wsAbs + string(filepath.Separator)
		return strings.HasPrefix(p, prefix)
	}

	winPaths := reWinAbs.FindAllString(cmd, -1)
	posixMatches := rePosixAbs.FindAllStringSubmatch(cmd, -1)
	posixPaths := make([]string, 0, len(posixMatches))
	for _, m := range posixMatches {
		if len(m) >= 3 {
			posixPaths = append(posixPaths, m[2])
		}
	}

	for _, raw := range append(winPaths, posixPaths...) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if isWithin(raw) {
			continue
		}
		return "Error: Command blocked by safety guard (path outside workspace)"
	}

	return ""
}
