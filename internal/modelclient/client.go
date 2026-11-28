// Package modelclient is the shell's own minimal LLM client. Its only
// inference call is Check, the settings page's connectivity probe when no
// harness host is online: one plain-text round trip against the configured
// endpoint, in either wire protocol (OpenAI-compatible chat completions or
// Anthropic Messages). Alongside it live the endpoint normalisation and the
// OpenCode special-casing shared with the settings page, the build-info probe
// and the model list.
//
// Real inference routing is the host's business: a running harness probes and
// chats through its own agent/provider, and the frontend tells the two probe
// answers apart by their `source` field. This package is the fallback, so it
// deliberately stays looser than the engine on three points (bare-host /v1
// appending, unknown provider names, a /messages suffix implying anthropic);
// drift_test.go pins each one with its engine-side citation.
//
// It reads the same environment variables as cyber-harness so a single
// configuration feeds both layers (the settings page reads the variables back
// and cmd/zrcin bridges its --model* flags into them):
//
//	CYBER_BASE_URL   http://host:8080      (a trailing /v1 is added if absent)
//	CYBER_PROVIDER   openai | anthropic
//	CYBER_API_KEY    sk-...
//	CYBER_MODEL      qwen3.8-27b
package modelclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	EnvBase  = "CYBER_BASE_URL"
	EnvKey   = "CYBER_API_KEY"
	EnvModel = "CYBER_MODEL"
)

// Provider is the wire protocol, not the vendor: DeepSeek, Moonshot, Ollama …
// are all OpenAI-compatible and map to ProviderOpenAI.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

// Usage is the token accounting the decision log records.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Result is one completion.
type Result struct {
	ReturnedModel string
	Content       string
	Usage         Usage
	Latency       time.Duration
	Model         string
}

// Client is one model endpoint.
type Client struct {
	// Provider is the protocol: ProviderOpenAI or ProviderAnthropic.
	Provider  string
	BaseURL   string
	APIKey    string
	Model     string
	HTTP      *http.Client
	MaxTokens int
	// SessionID belongs to this client's conversation, independent of its key.
	SessionID string
}

// normaliseBase accepts "http://h:8080" and "http://h:8080/v1" alike.
func normaliseBase(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, "/")
	if u == "" {
		return ""
	}
	if strings.HasSuffix(u, "/chat/completions") {
		u = strings.TrimSuffix(u, "/chat/completions")
	}
	if !strings.HasSuffix(u, "/v1") && !strings.Contains(u, "/v1/") {
		u += "/v1"
	}
	return u
}

// normaliseAnthropicBase mirrors the harness's Anthropic endpoint rule
// (base + "/messages") and only adds /v1 when the operator gave a bare host.
// A custom gateway path is left alone.
func normaliseAnthropicBase(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/messages")
	if u == "" {
		return ""
	}
	if parsed, err := url.Parse(u); err == nil && parsed.Host != "" && (parsed.Path == "" || parsed.Path == "/") {
		u = strings.TrimRight(u, "/") + "/v1"
	}
	return u
}

// providerOf resolves the protocol. Empty/unknown vendor names are
// OpenAI-compatible unless the URL itself is unmistakably Anthropic.
func providerOf(provider, base string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderAnthropic:
		return ProviderAnthropic
	case "", ProviderOpenAI:
		if strings.TrimSpace(provider) == "" {
			lower := strings.ToLower(base)
			if strings.Contains(lower, "anthropic.com") || strings.HasSuffix(strings.TrimRight(lower, "/"), "/messages") {
				return ProviderAnthropic
			}
		}
		return ProviderOpenAI
	default:
		// deepseek / moonshot / groq / openrouter / ollama / zhipu … all speak
		// the OpenAI wire format; provider is a protocol, not a brand.
		return ProviderOpenAI
	}
}

// Endpoint resolves the wire protocol and normalises the base URL exactly the
// way the client does. Auxiliary callers (the settings page asking for /models)
// use it so they cannot hit a different path than chat completions do: the
// harness base is commonly written as "http://host:8080" without "/v1".
func Endpoint(base, provider string) (string, string) {
	p := providerOf(provider, base)
	if p == ProviderAnthropic {
		return normaliseAnthropicBase(base), p
	}
	return normaliseBase(base), p
}

// NewWithProvider builds a client for the given protocol.
func NewWithProvider(base, key, model, provider string) (*Client, error) {
	p := providerOf(provider, base)
	if p == ProviderAnthropic {
		base = normaliseAnthropicBase(base)
	} else {
		base = normaliseBase(base)
	}
	key = strings.TrimSpace(key)
	model = strings.TrimSpace(model)
	switch {
	case base == "":
		return nil, fmt.Errorf("%s 为空：无法确定模型端点", EnvBase)
	case key == "":
		return nil, fmt.Errorf("%s 为空：无法鉴权", EnvKey)
	case model == "":
		return nil, fmt.Errorf("%s 为空：不知道该调哪个模型", EnvModel)
	}
	// Zero means "do not send max_tokens": the provider's model limit (or the
	// harness's own default) applies, so no invented cap lives here. Anthropic
	// is the exception its API requires a value; it keeps a conservative
	// default until the settings page sets one.
	maxTokens := 0
	if p == ProviderAnthropic {
		maxTokens = anthropicDefaultMaxToken
	}
	return &Client{
		Provider:  p,
		BaseURL:   base,
		APIKey:    key,
		Model:     model,
		HTTP:      &http.Client{Timeout: 300 * time.Second},
		MaxTokens: maxTokens,
		SessionID: "zrcin-" + rand.Text(),
	}, nil
}

// Message is one turn of a chat transcript.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

var thinkBlock = regexp.MustCompile(`(?is)<(think|thinking|analysis)>.*?</(think|thinking|analysis)>`)

// Check makes one plain-text inference without JSON mode or tool requirements.
func (c *Client) Check(ctx context.Context) (Result, error) {
	msgs := []Message{{Role: "user", Content: "Connectivity check. Reply only OK."}}
	if c.Provider == ProviderAnthropic {
		return c.chatAnthropic(ctx, msgs)
	}
	return c.chatOpenAI(ctx, msgs)
}

// chatOpenAI is Check's OpenAI leg: one plain-text, non-streaming round trip.
// No JSON mode and no invented cap — the probe must work against any
// OpenAI-compatible gateway, including ones that reject response_format, and
// an unset MaxTokens stays unset so the provider's own limit applies.
func (c *Client) chatOpenAI(ctx context.Context, msgs []Message) (Result, error) {
	max := c.MaxTokens
	reqBody := chatRequest{
		Model:     c.Model,
		Messages:  msgs,
		MaxTokens: max,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return Result{}, err
	}

	url := c.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	ApplyEndpointHeaders(req, c.SessionID)

	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("模型请求失败（%s）：%w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, err
	}
	latency := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		return Result{}, &APIError{StatusCode: resp.StatusCode, Body: clip(string(body), 400)}
	}
	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return Result{}, fmt.Errorf("模型响应不是 JSON：%w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return Result{}, fmt.Errorf("模型报错：%s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Result{}, fmt.Errorf("模型没有返回任何 choice")
	}
	msg := out.Choices[0].Message
	content := strings.TrimSpace(thinkBlock.ReplaceAllString(msg.Content, ""))
	if content == "" {
		if strings.TrimSpace(msg.ReasoningContent) != "" {
			return Result{}, fmt.Errorf("模型只输出了推理内容，没有正文（finish=%s）", out.Choices[0].FinishReason)
		}
		return Result{}, fmt.Errorf("模型返回空内容（finish=%s）", out.Choices[0].FinishReason)
	}
	// A reply that hit the token cap is a prefix of an answer, not an answer.
	// Say why instead of letting it read as a parse failure.
	if out.Choices[0].FinishReason == "length" {
		return Result{}, fmt.Errorf("模型输出被 max_tokens 截断（%d，推理与正文共用）：调大 max_tokens 或缩小输入", max)
	}
	model := out.Model
	if model == "" {
		model = c.Model
	}
	return Result{Content: content, Usage: out.Usage, Latency: latency, Model: model, ReturnedModel: out.Model}, nil
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
