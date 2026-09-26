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

	"translate_llm/internal/config"
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

// --- OpenAI-compatible base -----------------------------------------------

type openAICompatible struct {
	BaseURL string
	// useCacheControl adds OpenRouter's ephemeral cache block to the system message.
	useCacheControl bool
	// reasoningMarkers identifies CoT models where reasoning is disabled.
	reasoningMarkers []string
}

func (p *openAICompatible) isReasoning(modelID string) bool {
	lower := strings.ToLower(modelID)
	for _, m := range p.reasoningMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func (p *openAICompatible) GenerateCompletion(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	url := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	headers := map[string]string{
		"Authorization": "Bearer " + s.APIKey,
	}
	if strings.Contains(p.BaseURL, "openrouter.ai") {
		headers["HTTP-Referer"] = "https://pepin208.dedyn.io"
		headers["X-Title"] = "LLMT"
	}

	body := p.buildBody(payload, maxTokens, s)

	return executeWithRetry(ctx, func() (*CompletionResult, error) {
		raw, err := postJSON(ctx, url, headers, body)
		if err != nil {
			return nil, err
		}
		return parseChatCompletion(raw)
	})
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

// effectiveEffort resolves the session effort. Empty means "disabled/none".
// "auto" enables low effort only for models that advertise reasoning support.
func (p *openAICompatible) effectiveEffort(s *config.TranslationSession) string {
	if e := resolveEffort(s); e != "" {
		return e
	}
	if strings.EqualFold(strings.TrimSpace(s.ReasoningEffort), "auto") && p.isReasoning(s.ModelID) {
		return "low"
	}
	return ""
}

// isReasoningCapable reports whether the model can reason at all.
func (p *openAICompatible) isReasoningCapable(s *config.TranslationSession) bool {
	return p.isReasoning(s.ModelID) || s.ModelSupportsReasoning
}

// systemText combines the static system prompt with the per-file context block.
func systemText(s *config.TranslationSession) string {
	if s.ContextBlock == "" {
		return s.DynamicSystemPrompt
	}
	return s.DynamicSystemPrompt + "\n\n" + s.ContextBlock
}

// buildBody assembles the chat/completions request, applying OpenRouter routing
// preferences from the Web UI and the configured reasoning effort.
func (p *openAICompatible) buildBody(payload string, maxTokens int, s *config.TranslationSession) map[string]any {
	sys := systemText(s)
	var systemContent any = sys
	if p.useCacheControl {
		systemContent = []map[string]any{{
			"type":          "text",
			"text":          sys,
			"cache_control": map[string]string{"type": "ephemeral"},
		}}
	}

	body := map[string]any{
		"model":       s.ModelID,
		"temperature": 0,
		"max_tokens":  maxTokens,
		"messages": []map[string]any{
			{"role": "system", "content": systemContent},
			{"role": "user", "content": payload},
		},
	}

	// OpenRouter routing preferences driven by the Web UI. Ignored by other
	// OpenAI-compatible endpoints.
	if strings.Contains(p.BaseURL, "openrouter.ai") {
		routing := map[string]any{}
		if s.SelectedProvider != "" {
			routing["order"] = []string{s.SelectedProvider}
			routing["allow_fallbacks"] = true
		} else if s.ProviderSort != "" {
			routing["sort"] = s.ProviderSort
		}
		if s.AllowTraining {
			routing["data_collection"] = "allow"
		} else {
			routing["data_collection"] = "deny"
		}
		body["provider"] = routing
	}

	// Reasoning is explicit for OpenRouter on capable models: "none" must DISABLE
	// it, not omit it, otherwise reasoning models think by default and burn the
	// output budget on hidden tokens.
	effort := p.effectiveEffort(s)
	if strings.Contains(p.BaseURL, "openrouter.ai") {
		if p.isReasoningCapable(s) {
			if effort == "" {
				body["reasoning"] = map[string]any{"effort": "none"}
			} else {
				budget := reasoningBudget(effort)
				body["reasoning"] = map[string]any{"effort": effort, "max_tokens": budget}
				body["max_tokens"] = maxTokens + budget + 512
			}
		} else if effort != "" {
			budget := reasoningBudget(effort)
			body["reasoning"] = map[string]any{"effort": effort, "max_tokens": budget}
			body["max_tokens"] = maxTokens + budget + 512
		}
	} else if effort != "" {
		// OpenAI-compatible native parameter (o-series / GPT-5).
		body["reasoning_effort"] = effort
	}
	return body
}

func parseChatCompletion(raw map[string]any) (*CompletionResult, error) {
	choices, _ := raw["choices"].([]any)
	if len(choices) == 0 {
		return nil, fmt.Errorf("API returned invalid structure")
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	if msg == nil {
		return nil, fmt.Errorf("missing message in response")
	}
	content, _ := msg["content"].(string)
	if content == "" && choice["finish_reason"] == nil {
		return nil, fmt.Errorf("missing message content")
	}
	res := &CompletionResult{Content: content}
	if fr, ok := choice["finish_reason"].(string); ok {
		res.FinishReason = fr
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		res.PromptTokens = asInt(usage["prompt_tokens"])
		res.CompletionTokens = asInt(usage["completion_tokens"])
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			res.CachedTokens = asInt(details["cached_tokens"])
		}
	}
	return res, nil
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

// --- Concrete providers ----------------------------------------------------

// OpenRouterProvider talks to openrouter.ai (OpenAI-compatible).
type OpenRouterProvider struct{ openAICompatible }

// NewOpenRouterProvider builds the OpenRouter provider.
func NewOpenRouterProvider() *OpenRouterProvider {
	return &OpenRouterProvider{openAICompatible{
		BaseURL:         "https://openrouter.ai/api/v1",
		useCacheControl: true,
		reasoningMarkers: []string{
			"deepseek-r1", "deepseek-reasoner", "v4-flash", "-think", "qwq",
			"qvq", "o1-", "/o1", "o3-", "/o3", "r1-",
		},
	}}
}

// OpenAIProvider talks to api.openai.com.
type OpenAIProvider struct{ openAICompatible }

// NewOpenAIProvider builds the OpenAI provider.
func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{openAICompatible{BaseURL: "https://api.openai.com/v1"}}
}

// DeepSeekProvider talks to api.deepseek.com (OpenAI-compatible).
type DeepSeekProvider struct{ openAICompatible }

// NewDeepSeekProvider builds the DeepSeek provider.
func NewDeepSeekProvider() *DeepSeekProvider {
	return &DeepSeekProvider{openAICompatible{BaseURL: "https://api.deepseek.com/v1"}}
}

// LocalProvider talks to a user-supplied OpenAI-compatible server.
type LocalProvider struct {
	openAICompatible
}

// NewLocalProvider normalizes a user-entered base URL.
func NewLocalProvider(serverURL string) *LocalProvider {
	base := strings.TrimRight(serverURL, "/")
	switch {
	case strings.HasSuffix(base, "/v1/chat/completions"):
		base = strings.TrimSuffix(base, "/chat/completions")
	case strings.HasSuffix(base, "/v1"):
	case base == "":
		base = "http://localhost:11434/v1"
	default:
		base += "/v1"
	}
	return &LocalProvider{openAICompatible{BaseURL: base}}
}

// OpenCodeProvider talks to the OpenCode Zen/Go gateways and routes each model
// to the protocol its family uses.
type OpenCodeProvider struct {
	BaseURL          string
	openAICompatible // fallback for /chat/completions models
}

// NewOpenCodeProvider builds a provider for Zen (zen=true) or Go (zen=false).
func NewOpenCodeProvider(zen bool) *OpenCodeProvider {
	base := "https://opencode.ai/zen/go/v1"
	if zen {
		base = "https://opencode.ai/zen/v1"
	}
	return &OpenCodeProvider{
		BaseURL:          base,
		openAICompatible: openAICompatible{BaseURL: base},
	}
}

func (p *OpenCodeProvider) protocolFor(modelID string) string {
	lower := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(lower, "gpt-"), strings.HasPrefix(lower, "grok-"),
		strings.HasPrefix(lower, "muse-"):
		return "responses"
	case strings.HasPrefix(lower, "claude-"), strings.HasPrefix(lower, "qwen"):
		return "messages"
	case strings.HasPrefix(lower, "gemini-"):
		return "google"
	default:
		return "chat"
	}
}

// GenerateCompletion routes to the right OpenCode protocol.
func (p *OpenCodeProvider) GenerateCompletion(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	switch p.protocolFor(s.ModelID) {
	case "responses":
		return p.generateResponses(ctx, payload, maxTokens, s)
	case "messages":
		return generateAnthropicStyle(ctx, p.BaseURL+"/messages", "Bearer "+s.APIKey, payload, maxTokens, s)
	case "google":
		return p.generateGoogle(ctx, payload, maxTokens, s)
	default:
		return p.openAICompatible.GenerateCompletion(ctx, payload, maxTokens, s)
	}
}

func (p *OpenCodeProvider) generateResponses(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	body := map[string]any{
		"model":             s.ModelID,
		"instructions":      systemText(s),
		"input":             payload,
		"max_output_tokens": maxTokens,
		"temperature":       0,
	}
	if effort := resolveEffort(s); effort != "" {
		body["reasoning"] = map[string]string{"effort": effort}
	}
	headers := map[string]string{"Authorization": "Bearer " + s.APIKey}
	return executeWithRetry(ctx, func() (*CompletionResult, error) {
		raw, err := postJSON(ctx, p.BaseURL+"/responses", headers, body)
		if err != nil {
			return nil, err
		}
		return parseResponses(raw)
	})
}

func parseResponses(raw map[string]any) (*CompletionResult, error) {
	var text strings.Builder
	if output, ok := raw["output"].([]any); ok {
		for _, item := range output {
			m, _ := item.(map[string]any)
			if m == nil || m["type"] != "message" {
				continue
			}
			if content, ok := m["content"].([]any); ok {
				for _, c := range content {
					cm, _ := c.(map[string]any)
					if cm == nil {
						continue
					}
					if t, ok := cm["text"].(string); ok {
						text.WriteString(t)
					}
				}
			}
		}
	}
	if text.Len() == 0 {
		if ot, ok := raw["output_text"].(string); ok {
			text.WriteString(ot)
		}
	}
	res := &CompletionResult{Content: text.String()}
	if fr, ok := raw["status"].(string); ok {
		res.FinishReason = fr
	}
	if details, ok := raw["incomplete_details"].(map[string]any); ok {
		if r, ok := details["reason"].(string); ok {
			res.FinishReason = r
		}
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		res.PromptTokens = asInt(usage["input_tokens"])
		res.CompletionTokens = asInt(usage["output_tokens"])
	}
	return res, nil
}

func (p *OpenCodeProvider) generateGoogle(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	url := fmt.Sprintf("%s/models/%s:generateContent", p.BaseURL, s.ModelID)
	body := map[string]any{
		"system_instruction": map[string]any{"parts": []map[string]string{{"text": systemText(s)}}},
		"contents":           []map[string]any{{"parts": []map[string]string{{"text": payload}}}},
		"generationConfig":   map[string]any{"maxOutputTokens": maxTokens, "temperature": 0},
	}
	if budget := reasoningBudget(resolveEffort(s)); budget > 0 {
		body["generationConfig"] = map[string]any{"maxOutputTokens": maxTokens, "temperature": 0, "thinkingConfig": map[string]any{"thinkingBudget": budget}}
	}
	headers := map[string]string{"Authorization": "Bearer " + s.APIKey}
	return executeWithRetry(ctx, func() (*CompletionResult, error) {
		raw, err := postJSON(ctx, url, headers, body)
		if err != nil {
			return nil, err
		}
		return parseGoogle(raw)
	})
}

func parseGoogle(raw map[string]any) (*CompletionResult, error) {
	candidates, _ := raw["candidates"].([]any)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("empty/blocked Google response")
	}
	cand, _ := candidates[0].(map[string]any)
	content, _ := cand["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	var text strings.Builder
	for _, part := range parts {
		pm, _ := part.(map[string]any)
		if pm == nil {
			continue
		}
		if t, ok := pm["text"].(string); ok {
			text.WriteString(t)
		}
	}
	res := &CompletionResult{Content: text.String()}
	if fr, ok := cand["finishReason"].(string); ok {
		res.FinishReason = fr
	}
	if usage, ok := raw["usageMetadata"].(map[string]any); ok {
		res.PromptTokens = asInt(usage["promptTokenCount"])
		res.CompletionTokens = asInt(usage["candidatesTokenCount"])
	}
	return res, nil
}

// AnthropicProvider talks to api.anthropic.com.
type AnthropicProvider struct{}

// NewAnthropicProvider builds the Anthropic provider.
func NewAnthropicProvider() *AnthropicProvider { return &AnthropicProvider{} }

// GenerateCompletion implements the Anthropic Messages API.
func (p *AnthropicProvider) GenerateCompletion(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	headers := map[string]string{
		"x-api-key":         s.APIKey,
		"anthropic-version": "2023-06-01",
	}
	return executeWithRetry(ctx, func() (*CompletionResult, error) {
		return anthropicRequest(ctx, "https://api.anthropic.com/v1/messages", headers, payload, maxTokens, s)
	})
}

func generateAnthropicStyle(ctx context.Context, url, authHeader, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	headers := map[string]string{"Authorization": authHeader}
	return executeWithRetry(ctx, func() (*CompletionResult, error) {
		return anthropicRequest(ctx, url, headers, payload, maxTokens, s)
	})
}

func anthropicRequest(ctx context.Context, url string, headers map[string]string, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	body := map[string]any{
		"model":       s.ModelID,
		"max_tokens":  maxTokens,
		"temperature": 0,
		"system":      systemText(s),
		"messages":    []map[string]string{{"role": "user", "content": payload}},
	}
	if budget := reasoningBudget(resolveEffort(s)); budget > 0 {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		if maxTokens <= budget {
			body["max_tokens"] = budget + maxTokens
		}
	}
	raw, err := postJSON(ctx, url, headers, body)
	if err != nil {
		return nil, err
	}
	content, _ := raw["content"].([]any)
	var text strings.Builder
	for _, c := range content {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if t, ok := cm["text"].(string); ok {
			text.WriteString(t)
		}
	}
	res := &CompletionResult{Content: text.String()}
	if fr, ok := raw["stop_reason"].(string); ok {
		res.FinishReason = fr
	}
	if usage, ok := raw["usage"].(map[string]any); ok {
		res.PromptTokens = asInt(usage["input_tokens"])
		res.CompletionTokens = asInt(usage["output_tokens"])
	}
	return res, nil
}

// GeminiProvider talks to Google's generative language API directly.
type GeminiProvider struct{}

// NewGeminiProvider builds the Gemini provider.
func NewGeminiProvider() *GeminiProvider { return &GeminiProvider{} }

// GenerateCompletion implements the Google generateContent API.
func (p *GeminiProvider) GenerateCompletion(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent",
		s.ModelID,
	)
	headers := map[string]string{"x-goog-api-key": s.APIKey}
	return executeWithRetry(ctx, func() (*CompletionResult, error) {
		raw, err := postJSON(ctx, url, headers, geminiBody(payload, maxTokens, s))
		if err != nil {
			return nil, err
		}
		return parseGoogle(raw)
	})
}

func geminiBody(payload string, maxTokens int, s *config.TranslationSession) map[string]any {
	genConfig := map[string]any{"maxOutputTokens": maxTokens, "temperature": 0}
	if budget := reasoningBudget(resolveEffort(s)); budget > 0 {
		genConfig["thinkingConfig"] = map[string]any{"thinkingBudget": budget}
	}
	return map[string]any{
		"system_instruction": map[string]any{"parts": []map[string]string{{"text": systemText(s)}}},
		"contents":           []map[string]any{{"parts": []map[string]string{{"text": payload}}}},
		"generationConfig":   genConfig,
	}
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
