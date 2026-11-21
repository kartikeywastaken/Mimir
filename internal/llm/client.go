// Package llm is a minimal client for OpenAI-compatible chat completion APIs.
//
// Groq is the default provider, but any endpoint that implements
// POST {BaseURL}/chat/completions works (OpenRouter, Together, Ollama's
// /v1 shim, LM Studio, vLLM, ...).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.groq.com/openai/v1"
	// DefaultModel can be overridden with MIMIR_LLM_MODEL. Check the
	// provider's model list if this one has been retired.
	DefaultModel = "llama-3.3-70b-versatile"
)

// Config describes how to reach an OpenAI-compatible endpoint.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// ConfigFromEnv reads MIMIR_LLM_BASE_URL, MIMIR_LLM_MODEL and the API key from
// MIMIR_LLM_API_KEY (falling back to GROQ_API_KEY).
func ConfigFromEnv() Config {
	cfg := Config{
		BaseURL: firstNonEmpty(os.Getenv("MIMIR_LLM_BASE_URL"), DefaultBaseURL),
		APIKey:  firstNonEmpty(os.Getenv("MIMIR_LLM_API_KEY"), os.Getenv("GROQ_API_KEY")),
		Model:   firstNonEmpty(os.Getenv("MIMIR_LLM_MODEL"), DefaultModel),
		Timeout: 30 * time.Second,
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return cfg
}

// Configured reports whether the client has enough information to make calls.
// Local endpoints (e.g. Ollama) need no key, so a non-default base URL also counts.
func (c Config) Configured() bool {
	return c.APIKey != "" || (c.BaseURL != "" && c.BaseURL != DefaultBaseURL)
}

// Message is a single chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client sends chat completion requests.
type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

func (c *Client) Model() string { return c.cfg.Model }

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ChatJSON sends messages with JSON mode enabled and decodes the reply into out.
func (c *Client) ChatJSON(ctx context.Context, messages []Message, out any) error {
	content, err := c.chat(ctx, chatRequest{
		Model:          c.cfg.Model,
		Messages:       messages,
		Temperature:    0,
		ResponseFormat: &responseFormat{Type: "json_object"},
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(stripCodeFence(content)), out); err != nil {
		return fmt.Errorf("llm returned invalid JSON: %w | raw: %s", err, truncate(content, 200))
	}
	return nil
}

func (c *Client) chat(ctx context.Context, body chatRequest) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("llm read: %w", err)
	}

	var parsed chatResponse
	_ = json.Unmarshal(raw, &parsed)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(raw))
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return "", fmt.Errorf("llm %s: %s", resp.Status, truncate(msg, 300))
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
	}
	return strings.TrimSpace(s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
