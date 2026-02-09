package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

func (r *Registry) webSearch(ctx context.Context, query string, count int) (string, error) {
	if strings.TrimSpace(r.BraveAPIKey) == "" {
		return "", errors.New("braveApiKey not configured (config.tools.web.braveApiKey)")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is empty")
	}
	if count <= 0 || count > 10 {
		count = 5
	}
	u := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + fmt.Sprintf("&count=%d", count)
	req, err := retryablehttp.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", r.BraveAPIKey)

	rc := retryablehttp.NewClient()
	rc.RetryMax = 2
	rc.Logger = nil
	rc.HTTPClient.Timeout = 20 * time.Second
	resp, err := rc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("brave http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return formatBraveSearchResults(query, count, b), nil
}
