package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const defaultOpenAIAudioTranscriptionModel = "gpt-4o-mini-transcribe"

func (c *Client) SupportsAudioTranscription() bool {
	switch normalizeProvider(c.Provider) {
	case "openai", "openrouter", "ollama", "gemini":
		return true
	default:
		return false
	}
}

func (c *Client) SupportsImageInput() bool {
	provider := normalizeProvider(c.Provider)
	model := strings.ToLower(strings.TrimSpace(c.Model))
	switch provider {
	case "gemini":
		return true
	case "anthropic":
		return strings.Contains(model, "claude")
	case "openai", "openrouter", "ollama", "":
		return containsAny(model, []string{
			"gpt-4o",
			"gpt-4.1",
			"gpt-5",
			"vision",
			"vl",
			"llava",
			"pixtral",
			"gemini",
			"claude",
		})
	default:
		return false
	}
}

func (c *Client) TranscribeAudio(ctx context.Context, data []byte, mimeType, fileName string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("audio data is empty")
	}
	switch normalizeProvider(c.Provider) {
	case "openai", "openrouter", "ollama", "":
		return c.transcribeAudioOpenAICompatible(ctx, data, mimeType, fileName)
	case "gemini":
		return c.transcribeAudioGemini(ctx, data, mimeType)
	default:
		return "", fmt.Errorf("audio transcription is unsupported for provider: %s", strings.TrimSpace(c.Provider))
	}
}

func (c *Client) transcribeAudioOpenAICompatible(ctx context.Context, data []byte, mimeType, fileName string) (string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/") + "/audio/transcriptions"
	if strings.TrimSpace(c.BaseURL) == "" {
		return "", fmt.Errorf("baseURL is empty for audio transcription")
	}

	if strings.TrimSpace(fileName) == "" {
		fileName = "audio"
		if ext := extensionByMIME(mimeType); ext != "" {
			fileName += ext
		}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(fileName))
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := writer.WriteField("model", defaultOpenAIAudioTranscriptionModel); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("audio transcription http %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("parse transcription response: %w", err)
	}
	text := strings.TrimSpace(parsed.Text)
	if text == "" {
		return "", fmt.Errorf("audio transcription response is empty")
	}
	return text, nil
}

func (c *Client) transcribeAudioGemini(ctx context.Context, data []byte, mimeType string) (string, error) {
	endpoint := geminiGenerateContentEndpoint(c.BaseURL, c.Model)
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "audio/ogg"
	}

	prompt := "Transcribe the following audio. Return only the transcript text."
	zero := 0.0
	reqBody := struct {
		Contents         []geminiContent `json:"contents"`
		GenerationConfig struct {
			MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
			Temperature     *float64 `json:"temperature,omitempty"`
		} `json:"generationConfig"`
	}{
		Contents: []geminiContent{{
			Role: "user",
			Parts: []geminiPart{
				{Text: prompt},
				{InlineData: &geminiInlineData{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}},
			},
		}},
	}
	reqBody.GenerationConfig.MaxOutputTokens = c.maxTokensValue()
	reqBody.GenerationConfig.Temperature = &zero

	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("x-goog-api-key", c.APIKey)
	}
	for k, v := range c.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("audio transcription http %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text,omitempty"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason,omitempty"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("parse transcription response: %w", err)
	}
	if len(parsed.Candidates) == 0 {
		if strings.TrimSpace(parsed.PromptFeedback.BlockReason) != "" {
			return "", fmt.Errorf("gemini blocked: %s", parsed.PromptFeedback.BlockReason)
		}
		return "", fmt.Errorf("gemini response: no candidates")
	}

	chunks := make([]string, 0, len(parsed.Candidates[0].Content.Parts))
	for _, part := range parsed.Candidates[0].Content.Parts {
		if strings.TrimSpace(part.Text) != "" {
			chunks = append(chunks, strings.TrimSpace(part.Text))
		}
	}
	text := strings.TrimSpace(strings.Join(chunks, "\n"))
	if text == "" {
		return "", fmt.Errorf("audio transcription response is empty")
	}
	return text, nil
}

func containsAny(s string, needles []string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, n := range needles {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(n))) {
			return true
		}
	}
	return false
}

func extensionByMIME(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch mimeType {
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	default:
		return ""
	}
}
