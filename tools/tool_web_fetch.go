package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultWebFetchTimeoutSec  = 30
	defaultWebFetchBodyMaxSize = int64(4 << 20)
)

func (r *Registry) webFetch(ctx context.Context, rawURL string, extractMode string, maxChars int) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errors.New("url is empty")
	}
	pu, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if pu.Scheme != "http" && pu.Scheme != "https" {
		return "", fmt.Errorf("only http/https allowed: %s", pu.Scheme)
	}
	if strings.TrimSpace(pu.Host) == "" {
		return "", errors.New("missing host")
	}
	host := normalizeFetchHost(pu.Host)
	if host == "" {
		return "", errors.New("missing host")
	}
	if allowed, reason := allowHostByPolicy(host, r.WebFetchAllowedDomains, r.WebFetchBlockedDomains); !allowed {
		return "", fmt.Errorf("web_fetch blocked: %s", reason)
	}

	if strings.TrimSpace(extractMode) == "" {
		extractMode = "markdown"
	}
	if extractMode != "markdown" && extractMode != "text" {
		extractMode = "markdown"
	}
	if maxChars <= 0 {
		maxChars = 50000
	}
	if maxChars < 100 {
		maxChars = 100
	}

	timeout := r.WebFetchTimeout
	if timeout <= 0 {
		timeout = defaultWebFetchTimeoutSec * time.Second
	}
	maxBodyBytes := r.WebFetchMaxResponse
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultWebFetchBodyMaxSize
	}

	type outT struct {
		URL               string `json:"url"`
		FinalURL          string `json:"finalUrl,omitempty"`
		Status            int    `json:"status"`
		Extractor         string `json:"extractor"`
		Truncated         bool   `json:"truncated"`
		ResponseTruncated bool   `json:"responseTruncated,omitempty"`
		Length            int    `json:"length"`
		Text              string `json:"text"`
		Error             string `json:"error,omitempty"`
	}

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			rh := normalizeFetchHost(req.URL.Host)
			if allowed, reason := allowHostByPolicy(rh, r.WebFetchAllowedDomains, r.WebFetchBlockedDomains); !allowed {
				return fmt.Errorf("redirect blocked: %s", reason)
			}
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "clawlet/0.1")
	resp, err := client.Do(request)
	if err != nil {
		b, _ := json.Marshal(outT{URL: rawURL, Status: 0, Extractor: "error", Truncated: false, Length: 0, Text: "", Error: err.Error()})
		return string(b), nil
	}
	defer resp.Body.Close()

	finalURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	responseTruncated := int64(len(bodyBytes)) > maxBodyBytes
	if responseTruncated {
		bodyBytes = bodyBytes[:maxBodyBytes]
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))

	extractor := "raw"
	text := ""

	if strings.Contains(ct, "application/json") {
		var buf bytes.Buffer
		if err := json.Indent(&buf, bodyBytes, "", "  "); err == nil {
			text = buf.String()
			extractor = "json"
		} else {
			text = string(bodyBytes)
		}
	} else if strings.Contains(ct, "text/html") || looksLikeHTML(bodyBytes) {
		extractor = "html"
		title, plain := extractHTMLText(string(bodyBytes))
		if extractMode == "markdown" {
			if strings.TrimSpace(title) != "" {
				text = "# " + strings.TrimSpace(title) + "\n\n" + plain
			} else {
				text = plain
			}
		} else {
			text = plain
		}
	} else {
		text = strings.TrimSpace(string(bodyBytes))
	}

	outputTruncated := responseTruncated
	if len(text) > maxChars {
		outputTruncated = true
		text = text[:maxChars]
	}

	errText := ""
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errText = fmt.Sprintf("http %d", resp.StatusCode)
	}

	o := outT{
		URL:               rawURL,
		FinalURL:          finalURL,
		Status:            resp.StatusCode,
		Extractor:         extractor,
		Truncated:         outputTruncated,
		ResponseTruncated: responseTruncated,
		Length:            len(text),
		Text:              text,
		Error:             errText,
	}
	b, _ := json.Marshal(o)
	return string(b), nil
}
