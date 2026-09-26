package providers

import (
	"context"
	"strings"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

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
		return anthropicRequest(ctx, config.AnthropicMessagesURL, headers, payload, maxTokens, s)
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
