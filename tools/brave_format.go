package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

func formatBraveSearchResults(query string, count int, body []byte) string {
	type item struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	var parsed struct {
		Web struct {
			Results []item `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "Error: failed to parse search results"
	}
	results := parsed.Web.Results
	if len(results) == 0 {
		return fmt.Sprintf("No results for: %s", query)
	}
	if count <= 0 || count > 10 {
		count = 5
	}
	if len(results) > count {
		results = results[:count]
	}
	lines := []string{fmt.Sprintf("Results for: %s\n", query)}
	for i, it := range results {
		title := strings.TrimSpace(it.Title)
		url := strings.TrimSpace(it.URL)
		desc := strings.TrimSpace(it.Description)
		if title == "" {
			title = "(no title)"
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, title, url))
		if desc != "" {
			lines = append(lines, "   "+desc)
		}
	}
	return strings.Join(lines, "\n")
}
