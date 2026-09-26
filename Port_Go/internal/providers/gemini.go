package providers

import (
	"context"
	"fmt"

	"translate_llm/internal/config"
)

// GeminiProvider talks to Google's generative language API directly.
type GeminiProvider struct{}

// NewGeminiProvider builds the Gemini provider.
func NewGeminiProvider() *GeminiProvider { return &GeminiProvider{} }

// GenerateCompletion implements the Google generateContent API.
func (p *GeminiProvider) GenerateCompletion(ctx context.Context, payload string, maxTokens int, s *config.TranslationSession) (*CompletionResult, error) {
	url := fmt.Sprintf(
		"%s/models/%s:generateContent",
		config.GeminiBaseURL, s.ModelID,
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
