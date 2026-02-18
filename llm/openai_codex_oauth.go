package llm

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/paths"
)

const (
	codexOAuthClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexOAuthIssuer      = "https://auth.openai.com"
	codexOAuthAuthorize   = "https://auth.openai.com/oauth/authorize"
	codexOAuthTokenURL    = "https://auth.openai.com/oauth/token"
	codexOAuthRedirectURI = "http://localhost:1455/auth/callback"
	codexOAuthScope       = "openid profile email offline_access"
	codexOAuthOriginator  = "codex_cli_rs"
	codexJWTClaimPath     = "https://api.openai.com/auth"
	codexTokenFileName    = "codex.json"
	codexMinTTLSeconds    = int64(60)
)

const codexOAuthSuccessHTML = "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\" /><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" /><title>Authentication successful</title></head><body><p>Authentication successful. Return to your terminal to continue.</p></body></html>"

type CodexOAuthToken struct {
	AccessToken string
	AccountID   string
}

func (t CodexOAuthToken) Valid() bool {
	return strings.TrimSpace(t.AccessToken) != "" && strings.TrimSpace(t.AccountID) != ""
}

type codexStoredToken struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`
	AccountID string `json:"account_id,omitempty"`
}

type codexDeviceCodeResponse struct {
	DeviceAuthID string
	UserCode     string
	IntervalSec  int
	ExpiresInSec int
}

var errCodexDeviceAuthPending = errors.New("device authorization pending")

func LoadCodexOAuthToken() (CodexOAuthToken, error) {
	tok, err := getCodexToken(codexMinTTLSeconds)
	if err != nil {
		return CodexOAuthToken{}, err
	}
	out := CodexOAuthToken{AccessToken: tok.Access, AccountID: tok.AccountID}
	if !out.Valid() {
		return CodexOAuthToken{}, fmt.Errorf("codex oauth token is invalid; run `clawlet provider login openai-codex`")
	}
	return out, nil
}

func LoginCodexOAuthInteractive(ctx context.Context) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return err
	}
	state, err := createState()
	if err != nil {
		return err
	}

	authURL := buildCodexAuthorizeURL(state, challenge)
	fmt.Println("Open the following URL in your browser if it does not open automatically:")
	fmt.Println(authURL)
	_ = openBrowser(authURL)

	codeCh := make(chan string, 1)
	server, serverErr := startCodexLocalServer(state, codeCh)
	if serverErr != nil {
		fmt.Printf("warning: local callback server could not start (%v)\n", serverErr)
	}

	if server != nil {
		defer server.Close()
		fmt.Println("Waiting for browser callback...")
	}

	code := ""
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if server != nil {
		select {
		case code = <-codeCh:
		case <-waitCtx.Done():
		}
	}

	if strings.TrimSpace(code) == "" {
		fmt.Print("Paste the callback URL or authorization code: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read authorization input: %w", err)
		}
		parsedCode, parsedState := parseAuthorizationInput(line)
		if parsedState != "" && parsedState != state {
			return fmt.Errorf("oauth state validation failed")
		}
		code = parsedCode
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("authorization code not found")
	}

	fmt.Println("Exchanging authorization code for tokens...")
	tok, err := exchangeAuthorizationCode(ctx, code, verifier, codexOAuthRedirectURI)
	if err != nil {
		return err
	}
	if err := saveStoredCodexToken(tok); err != nil {
		return err
	}
	return nil
}

func LoginCodexOAuthDeviceCode(ctx context.Context) error {
	device, err := requestCodexDeviceCode(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\nTo authenticate, open this URL in your browser:\n\n  %s/codex/device\n\nThen enter this code: %s\n\nWaiting for authentication...\n",
		codexOAuthIssuer, device.UserCode)

	tok, err := pollCodexDeviceCode(ctx, device)
	if err != nil {
		return err
	}
	if err := saveStoredCodexToken(tok); err != nil {
		return err
	}
	return nil
}

func getCodexToken(minTTLSeconds int64) (codexStoredToken, error) {
	tok, err := loadStoredCodexToken()
	if err != nil {
		return codexStoredToken{}, err
	}
	nowMs := time.Now().UnixMilli()
	if tok.Expires-nowMs > minTTLSeconds*1000 {
		return tok, nil
	}

	refreshed, err := refreshCodexToken(tok.Refresh)
	if err != nil {
		latest, loadErr := loadStoredCodexToken()
		if loadErr == nil && latest.Expires-time.Now().UnixMilli() > 0 {
			return latest, nil
		}
		return codexStoredToken{}, err
	}
	if strings.TrimSpace(refreshed.AccountID) == "" {
		refreshed.AccountID = tok.AccountID
	}
	if err := saveStoredCodexToken(refreshed); err != nil {
		return codexStoredToken{}, err
	}
	return refreshed, nil
}

func exchangeAuthorizationCode(ctx context.Context, code, verifier, redirectURI string) (codexStoredToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", codexOAuthClientID)
	form.Set("code", strings.TrimSpace(code))
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return codexStoredToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return codexStoredToken{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return codexStoredToken{}, fmt.Errorf("token exchange failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseTokenPayload(body, "token exchange response missing fields", true)
}

func requestCodexDeviceCode(ctx context.Context) (codexDeviceCodeResponse, error) {
	reqBody, err := json.Marshal(map[string]string{
		"client_id": codexOAuthClientID,
	})
	if err != nil {
		return codexDeviceCodeResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/api/accounts/deviceauth/usercode", strings.NewReader(string(reqBody)))
	if err != nil {
		return codexDeviceCodeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return codexDeviceCodeResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return codexDeviceCodeResponse{}, fmt.Errorf("device code request failed: %s", strings.TrimSpace(string(body)))
	}
	return parseDeviceCodeResponse(body)
}

func parseDeviceCodeResponse(body []byte) (codexDeviceCodeResponse, error) {
	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
		ExpiresIn    json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return codexDeviceCodeResponse{}, err
	}
	intervalSec, err := parseFlexibleInt(raw.Interval)
	if err != nil {
		return codexDeviceCodeResponse{}, err
	}
	if intervalSec < 1 {
		intervalSec = 5
	}
	expiresInSec, err := parseFlexibleInt(raw.ExpiresIn)
	if err != nil {
		return codexDeviceCodeResponse{}, err
	}
	// Fallback to a practical timeout when server doesn't return expires_in.
	if expiresInSec < 60 {
		expiresInSec = 30 * 60
	}
	if strings.TrimSpace(raw.DeviceAuthID) == "" || strings.TrimSpace(raw.UserCode) == "" {
		return codexDeviceCodeResponse{}, fmt.Errorf("device code response missing fields")
	}
	return codexDeviceCodeResponse{
		DeviceAuthID: raw.DeviceAuthID,
		UserCode:     raw.UserCode,
		IntervalSec:  intervalSec,
		ExpiresInSec: expiresInSec,
	}, nil
}

func parseFlexibleInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var v int
	if err := json.Unmarshal(raw, &v); err == nil {
		return v, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, nil
		}
		return strconv.Atoi(s)
	}
	return 0, fmt.Errorf("invalid integer value: %s", string(raw))
}

func pollCodexDeviceCode(ctx context.Context, device codexDeviceCodeResponse) (codexStoredToken, error) {
	deadline := time.NewTimer(time.Duration(device.ExpiresInSec) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Duration(device.IntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return codexStoredToken{}, ctx.Err()
		case <-deadline.C:
			return codexStoredToken{}, fmt.Errorf("device code authentication timed out")
		case <-ticker.C:
			tok, done, err := tryPollCodexDeviceCode(ctx, device.DeviceAuthID, device.UserCode)
			if err != nil {
				if errors.Is(err, errCodexDeviceAuthPending) {
					continue
				}
				return codexStoredToken{}, err
			}
			if done {
				return tok, nil
			}
		}
	}
}

func tryPollCodexDeviceCode(ctx context.Context, deviceAuthID, userCode string) (codexStoredToken, bool, error) {
	reqBody, err := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})
	if err != nil {
		return codexStoredToken{}, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthIssuer+"/api/accounts/deviceauth/token", strings.NewReader(string(reqBody)))
	if err != nil {
		return codexStoredToken{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return codexStoredToken{}, false, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		if codexDeviceAuthIsPending(body) {
			return codexStoredToken{}, false, errCodexDeviceAuthPending
		}
		return codexStoredToken{}, false, fmt.Errorf("device auth token request failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return codexStoredToken{}, false, err
	}
	if strings.TrimSpace(tokenResp.AuthorizationCode) == "" || strings.TrimSpace(tokenResp.CodeVerifier) == "" {
		return codexStoredToken{}, false, fmt.Errorf("device auth token response missing fields")
	}

	tok, err := exchangeAuthorizationCode(ctx, tokenResp.AuthorizationCode, tokenResp.CodeVerifier, codexOAuthIssuer+"/deviceauth/callback")
	if err != nil {
		return codexStoredToken{}, false, err
	}
	return tok, true, nil
}

func codexDeviceAuthIsPending(body []byte) bool {
	raw := strings.ToLower(strings.TrimSpace(string(body)))
	if raw == "" {
		return true
	}
	if strings.Contains(raw, "pending") ||
		strings.Contains(raw, "authorization_pending") ||
		strings.Contains(raw, "slow_down") ||
		strings.Contains(raw, "deviceauth_authorization_unknown") ||
		strings.Contains(raw, "device authorization is unknown") {
		return true
	}
	var payload struct {
		ErrorCode        string          `json:"error_code"`
		ErrorDescription string          `json:"error_description"`
		Message          string          `json:"message"`
		ErrorRaw         json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		var errorText string
		var errorObj struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(payload.ErrorRaw, &errorText)
		_ = json.Unmarshal(payload.ErrorRaw, &errorObj)
		for _, v := range []string{
			errorText,
			payload.ErrorCode,
			payload.ErrorDescription,
			payload.Message,
			errorObj.Message,
			errorObj.Type,
			errorObj.Code,
		} {
			l := strings.ToLower(strings.TrimSpace(v))
			if strings.Contains(l, "pending") ||
				strings.Contains(l, "authorization_pending") ||
				strings.Contains(l, "slow_down") ||
				strings.Contains(l, "deviceauth_authorization_unknown") ||
				strings.Contains(l, "device authorization is unknown") {
				return true
			}
		}
	}
	return false
}

func refreshCodexToken(refreshToken string) (codexStoredToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	form.Set("client_id", codexOAuthClientID)

	req, err := http.NewRequest(http.MethodPost, codexOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return codexStoredToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return codexStoredToken{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return codexStoredToken{}, fmt.Errorf("token refresh failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	tok, err := parseTokenPayload(body, "token refresh response missing fields", false)
	if err != nil {
		return codexStoredToken{}, err
	}
	if strings.TrimSpace(tok.Refresh) == "" {
		tok.Refresh = strings.TrimSpace(refreshToken)
	}
	return tok, nil
}

func parseTokenPayload(body []byte, missingErr string, requireRefreshToken bool) (codexStoredToken, error) {
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return codexStoredToken{}, err
	}
	if strings.TrimSpace(payload.AccessToken) == "" || payload.ExpiresIn <= 0 {
		return codexStoredToken{}, errors.New(missingErr)
	}
	if requireRefreshToken && strings.TrimSpace(payload.RefreshToken) == "" {
		return codexStoredToken{}, errors.New(missingErr)
	}
	accountID := decodeCodexAccountID(payload.IDToken)
	if strings.TrimSpace(accountID) == "" {
		accountID = decodeCodexAccountID(payload.AccessToken)
	}
	return codexStoredToken{
		Access:    payload.AccessToken,
		Refresh:   payload.RefreshToken,
		Expires:   time.Now().UnixMilli() + payload.ExpiresIn*1000,
		AccountID: accountID,
	}, nil
}

func decodeCodexAccountID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return ""
	}

	if accountID := rawJSONFieldString(payload["chatgpt_account_id"]); strings.TrimSpace(accountID) != "" {
		return accountID
	}
	if accountID := rawJSONFieldString(payload["https://api.openai.com/auth.chatgpt_account_id"]); strings.TrimSpace(accountID) != "" {
		return accountID
	}
	if authRaw, ok := payload[codexJWTClaimPath]; ok {
		var auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		}
		if err := json.Unmarshal(authRaw, &auth); err == nil && strings.TrimSpace(auth.ChatGPTAccountID) != "" {
			return auth.ChatGPTAccountID
		}
	}
	if orgsRaw, ok := payload["organizations"]; ok {
		var orgs []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(orgsRaw, &orgs); err == nil {
			for _, org := range orgs {
				if strings.TrimSpace(org.ID) != "" {
					return org.ID
				}
			}
		}
	}
	return ""
}

func rawJSONFieldString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return out
}

func buildCodexAuthorizeURL(state, challenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", codexOAuthClientID)
	q.Set("redirect_uri", codexOAuthRedirectURI)
	q.Set("scope", codexOAuthScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", codexOAuthOriginator)
	return codexOAuthAuthorize + "?" + q.Encode()
}

func generatePKCE() (verifier string, challenge string, err error) {
	rnd := make([]byte, 32)
	if _, err := rand.Read(rnd); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(rnd)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func createState() (string, error) {
	rnd := make([]byte, 16)
	if _, err := rand.Read(rnd); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(rnd), nil
}

func parseAuthorizationInput(raw string) (code string, state string) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", ""
	}
	if u, err := url.Parse(v); err == nil && u.RawQuery != "" {
		q := u.Query()
		if q.Get("code") != "" {
			return q.Get("code"), q.Get("state")
		}
	}
	if strings.Contains(v, "#") {
		parts := strings.SplitN(v, "#", 2)
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if strings.Contains(v, "code=") {
		if q, err := url.ParseQuery(v); err == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return v, ""
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func startCodexLocalServer(expectedState string, codeCh chan<- string) (io.Closer, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != expectedState {
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if strings.TrimSpace(code) == "" {
			http.Error(w, "Missing code", http.StatusBadRequest)
			return
		}

		select {
		case codeCh <- code:
		default:
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte(codexOAuthSuccessHTML))
	})

	ln, err := net.Listen("tcp", "localhost:1455")
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return closerFunc(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}), nil
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func loadStoredCodexToken() (codexStoredToken, error) {
	path, err := codexTokenPath()
	if err != nil {
		return codexStoredToken{}, err
	}
	tok, err := readStoredCodexToken(path)
	if err == nil {
		return tok, nil
	}

	imported, importErr := importFromCodexCLI(path)
	if importErr == nil {
		return imported, nil
	}
	return codexStoredToken{}, fmt.Errorf("oauth credentials not found; run `clawlet provider login openai-codex`")
}

func readStoredCodexToken(path string) (codexStoredToken, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return codexStoredToken{}, err
	}
	var tok codexStoredToken
	if err := json.Unmarshal(b, &tok); err != nil {
		return codexStoredToken{}, err
	}
	if strings.TrimSpace(tok.Access) == "" || strings.TrimSpace(tok.Refresh) == "" || tok.Expires <= 0 {
		return codexStoredToken{}, fmt.Errorf("invalid token file")
	}
	return tok, nil
}

func importFromCodexCLI(destPath string) (codexStoredToken, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(userHomeDir(), ".codex")
	}
	codexPath := filepath.Join(codexHome, "auth.json")
	b, err := os.ReadFile(codexPath)
	if err != nil {
		return codexStoredToken{}, err
	}
	var parsed struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return codexStoredToken{}, err
	}
	if parsed.Tokens.AccessToken == "" || parsed.Tokens.RefreshToken == "" || parsed.Tokens.AccountID == "" {
		return codexStoredToken{}, fmt.Errorf("invalid codex auth format")
	}
	expires := time.Now().UnixMilli() + int64(time.Hour/time.Millisecond)
	if st, err := os.Stat(codexPath); err == nil {
		expires = st.ModTime().UnixMilli() + int64(time.Hour/time.Millisecond)
	}
	tok := codexStoredToken{
		Access:    parsed.Tokens.AccessToken,
		Refresh:   parsed.Tokens.RefreshToken,
		Expires:   expires,
		AccountID: parsed.Tokens.AccountID,
	}
	if err := writeStoredCodexToken(destPath, tok); err != nil {
		return codexStoredToken{}, err
	}
	return tok, nil
}

func saveStoredCodexToken(tok codexStoredToken) error {
	path, err := codexTokenPath()
	if err != nil {
		return err
	}
	return writeStoredCodexToken(path, tok)
}

func writeStoredCodexToken(path string, tok codexStoredToken) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func codexTokenPath() (string, error) {
	cfgDir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "auth", codexTokenFileName), nil
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
