package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAllowHostByPolicy_DefaultAllowAll(t *testing.T) {
	ok, reason := allowHostByPolicy("example.com", nil, nil)
	if !ok {
		t.Fatalf("expected allowed, reason=%s", reason)
	}
}

func TestAllowHostByPolicy_BlockedTakesPrecedence(t *testing.T) {
	ok, reason := allowHostByPolicy("api.example.com", []string{"*"}, []string{"example.com"})
	if ok {
		t.Fatalf("expected blocked")
	}
	if !strings.Contains(reason, "blocked") {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestAllowHostByPolicy_AllowSubdomain(t *testing.T) {
	ok, reason := allowHostByPolicy("api.example.com", []string{"example.com"}, nil)
	if !ok {
		t.Fatalf("expected allowed, reason=%s", reason)
	}
}

func TestAllowHostByPolicy_EmptyAllowListDenies(t *testing.T) {
	ok, reason := allowHostByPolicy("example.com", []string{}, nil)
	if ok {
		t.Fatalf("expected denied")
	}
	if !strings.Contains(reason, "no allowed domains") {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestWebFetch_RespectsResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 4096)))
	}))
	defer server.Close()

	r := &Registry{
		WebFetchAllowedDomains: []string{"*"},
		WebFetchBlockedDomains: nil,
		WebFetchMaxResponse:    256,
		WebFetchTimeout:        5 * time.Second,
	}

	out, err := r.webFetch(context.Background(), server.URL, "text", 10000)
	if err != nil {
		t.Fatalf("webFetch failed: %v", err)
	}
	var payload struct {
		Truncated         bool `json:"truncated"`
		ResponseTruncated bool `json:"responseTruncated"`
		Length            int  `json:"length"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if !payload.Truncated || !payload.ResponseTruncated {
		t.Fatalf("expected truncation flags, got %+v", payload)
	}
	if payload.Length > 256 {
		t.Fatalf("length exceeds response limit: %d", payload.Length)
	}
}

func TestWebFetch_DomainPolicyBlocks(t *testing.T) {
	r := &Registry{WebFetchAllowedDomains: []string{"example.com"}}
	_, err := r.webFetch(context.Background(), "https://openai.com", "text", 200)
	if err == nil {
		t.Fatalf("expected policy error")
	}
	if !strings.Contains(err.Error(), "not in allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
