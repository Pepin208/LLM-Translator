package providers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

// openAICompatible is the shared base for every provider that speaks the
// OpenAI chat/completions shape (OpenRouter, OpenAI, DeepSeek, Local and the
// OpenCode fallback).
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
	if strings.Contains(p.BaseURL, config.OpenRouterHost) {
		headers["HTTP-Referer"] = config.RefererURL()
		headers["X-Title"] = config.AppTitle
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
	if strings.Contains(p.BaseURL, config.OpenRouterHost) {
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
	if strings.Contains(p.BaseURL, config.OpenRouterHost) {
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
