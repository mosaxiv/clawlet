package tools

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n(truncated)"
}
