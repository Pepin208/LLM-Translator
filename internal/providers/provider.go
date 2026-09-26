// Package providers implements the LLM provider abstraction: each provider
// handles its own auth, endpoint and response shape, normalizing to a single
// CompletionResult so the engine stays provider-agnostic.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

// CompletionResult is the normalized provider response.
type CompletionResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	// FinishReason is the provider's stop reason ("stop", "length", ...).
	FinishReason string
}

// Truncated reports whether the response hit the output token limit.
func (r *CompletionResult) Truncated() bool {
	switch strings.ToLower(r.FinishReason) {
	case "length", "max_tokens", "max_output_tokens", "incomplete":
		return true
	}
	return false
}

// Provider generates a completion from a payload string.
type Provider interface {
	GenerateCompletion(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error)
}

// HTTPError carries a non-2xx status so the retry loop can classify it.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

var (
	throttleMu      sync.Mutex
	lastRequestTime time.Time
)

func throttle() {
	throttleMu.Lock()
	defer throttleMu.Unlock()
	elapsed := time.Since(lastRequestTime).Seconds()
	if elapsed < config.MinRequestDelay {
		time.Sleep(time.Duration((config.MinRequestDelay - elapsed) * float64(time.Second)))
	}
	lastRequestTime = time.Now()
}

func isRetryable(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 408 || he.Status == 429 || (he.Status >= 500 && he.Status < 600)
	}
	// Transport errors (no status) are transient.
	return true
}

// executeWithRetry runs fn with throttling and exponential backoff.
func executeWithRetry(ctx context.Context, fn func() (*CompletionResult, error)) (*CompletionResult, error) {
	var lastErr error
	for attempt := 0; attempt < config.MaxRetries; attempt++ {
		throttle()
		res, err := fn()
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
		if attempt >= config.MaxRetries-1 {
			break
		}
		delay := config.RetrySleep * float64(int(1)<<attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(delay * float64(time.Second))):
		}
	}
	return nil, lastErr
}

// --- HTTP helper -----------------------------------------------------------

func postJSON(ctx context.Context, url string, headers map[string]string, body any) (map[string]any, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: config.RequestTimeout * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Body: truncate(string(data), 500)}
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// --- Shared prompt/reasoning helpers ---------------------------------------

// systemText combines the static system prompt with the per-file context block.
func systemText(s *config.TranslationSession) string {
	if s.ContextBlock == "" {
		return s.DynamicSystemPrompt
	}
	return s.DynamicSystemPrompt + "\n\n" + s.ContextBlock
}

// reasoningBudget returns the thinking token budget for an effort level.
func reasoningBudget(effort string) int {
	switch strings.ToLower(effort) {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	}
	return 0
}

// resolveEffort resolves the session effort for non-OpenAI-compatible providers.
func resolveEffort(s *config.TranslationSession) string {
	e := strings.ToLower(strings.TrimSpace(s.ReasoningEffort))
	if e == "" || e == "auto" {
		if s.ModelSupportsReasoning {
			return "low"
		}
		return ""
	}
	if e == "none" {
		return ""
	}
	return e
}

// --- Factory ---------------------------------------------------------------

// CreateProvider instantiates a provider by display name.
func CreateProvider(name, localURL string) (Provider, error) {
	switch name {
	case "OpenRouter":
		return NewOpenRouterProvider(), nil
	case "OpenAI":
		return NewOpenAIProvider(), nil
	case "DeepSeek":
		return NewDeepSeekProvider(), nil
	case "Anthropic":
		return NewAnthropicProvider(), nil
	case "Google Gemini":
		return NewGeminiProvider(), nil
	case "Local":
		if strings.TrimSpace(localURL) == "" {
			return nil, fmt.Errorf("a server URL is required for the Local provider")
		}
		return NewLocalProvider(localURL), nil
	case "OpenCode Zen":
		return NewOpenCodeProvider(true), nil
	case "OpenCode Go":
		return NewOpenCodeProvider(false), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
}
