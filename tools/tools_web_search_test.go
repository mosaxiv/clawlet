package tools

import (
	"strings"
	"testing"
)

func TestFormatBraveSearchResults(t *testing.T) {
	body := []byte(`{
  "web": {
    "results": [
      { "title": "Example", "url": "https://example.com", "description": "An example page." },
      { "title": "Second", "url": "https://example.org", "description": "" }
    ]
  }
}`)
	out := formatBraveSearchResults("test query", 5, body)
	if out == "" {
		t.Fatalf("expected non-empty output")
	}
	if want := "Results for: test query"; !strings.Contains(out, want) {
		t.Fatalf("missing header: %q\nout=%q", want, out)
	}
	if !strings.Contains(out, "1. Example") || !strings.Contains(out, "https://example.com") {
		t.Fatalf("missing first result\nout=%q", out)
	}
	if !strings.Contains(out, "2. Second") || !strings.Contains(out, "https://example.org") {
		t.Fatalf("missing second result\nout=%q", out)
	}
}

func TestFormatBraveSearchResults_NoResults(t *testing.T) {
	body := []byte(`{ "web": { "results": [] } }`)
	out := formatBraveSearchResults("zzz", 5, body)
	if out != "No results for: zzz" {
		t.Fatalf("unexpected: %q", out)
	}
}
