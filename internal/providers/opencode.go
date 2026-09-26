package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

// OpenCodeProvider talks to the OpenCode Zen/Go gateways and routes each model
// to the protocol its family uses.
type OpenCodeProvider struct {
	BaseURL          string
	openAICompatible // fallback for /chat/completions models
}

// NewOpenCodeProvider builds a provider for Zen (zen=true) or Go (zen=false).
func NewOpenCodeProvider(zen bool) *OpenCodeProvider {
	base := config.OpenCodeGoBaseURL
	if zen {
		base = config.OpenCodeZenBaseURL
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
