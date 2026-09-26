package providers

import (
	"errors"
	"strings"
	"testing"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

func TestOpenCodeProtocolRouting(t *testing.T) {
	p := NewOpenCodeProvider(true)
	cases := map[string]string{
		"gpt-5.5":           "responses",
		"grok-4.6":          "responses",
		"claude-haiku-4-5":  "messages",
		"qwen3.7-max":       "messages",
		"gemini-3-flash":    "google",
		"deepseek-v4-flash": "chat",
	}
	for model, want := range cases {
		if got := p.protocolFor(model); got != want {
			t.Errorf("protocolFor(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestParseChatCompletion(t *testing.T) {
	raw := map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{"content": "1|Hola"},
		}},
		"usage": map[string]any{
			"prompt_tokens":         float64(10),
			"completion_tokens":     float64(5),
			"prompt_tokens_details": map[string]any{"cached_tokens": float64(3)},
		},
	}
	res, err := parseChatCompletion(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "1|Hola" || res.PromptTokens != 10 || res.CompletionTokens != 5 || res.CachedTokens != 3 {
		t.Errorf("result = %+v", res)
	}
}

func TestParseGoogle(t *testing.T) {
	raw := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]any{"text": "Hola"}}},
		}},
		"usageMetadata": map[string]any{"promptTokenCount": float64(4), "candidatesTokenCount": float64(2)},
	}
	res, err := parseGoogle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hola" || res.PromptTokens != 4 || res.CompletionTokens != 2 {
		t.Errorf("result = %+v", res)
	}
}

func TestParseResponses(t *testing.T) {
	raw := map[string]any{
		"output": []any{map[string]any{
			"type":    "message",
			"content": []any{map[string]any{"type": "output_text", "text": "Hola"}},
		}},
		"usage": map[string]any{"input_tokens": float64(7), "output_tokens": float64(3)},
	}
	res, err := parseResponses(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hola" || res.PromptTokens != 7 || res.CompletionTokens != 3 {
		t.Errorf("result = %+v", res)
	}
}

func TestReasoningEffortMapping(t *testing.T) {
	p := NewOpenRouterProvider()
	s := config.NewSession()
	s.ModelID = "deepseek/deepseek-v4-flash"
	s.DynamicSystemPrompt = "sp"

	body := p.buildBody("x", 600, s)
	r, ok := body["reasoning"].(map[string]any)
	if !ok || r["effort"] != "low" {
		t.Fatalf("auto reasoning = %v", body["reasoning"])
	}
	if body["max_tokens"] != 600+2048+512 {
		t.Errorf("max_tokens = %v, want 3160", body["max_tokens"])
	}

	// "none" must explicitly disable reasoning, not omit the parameter.
	s.ReasoningEffort = "none"
	noneBody := p.buildBody("x", 600, s)
	noneReasoning, ok := noneBody["reasoning"].(map[string]any)
	if !ok || noneReasoning["effort"] != "none" {
		t.Errorf("none must explicitly disable reasoning, got %v", noneBody["reasoning"])
	}
	if noneBody["max_tokens"] != 600 {
		t.Errorf("none should not grow max_tokens: %v", noneBody["max_tokens"])
	}

	s.ReasoningEffort = "high"
	if r := p.buildBody("x", 600, s)["reasoning"].(map[string]any); r["effort"] != "high" {
		t.Errorf("high effort = %v", r["effort"])
	}
}

func TestNonReasoningModelHasNoReasoningParam(t *testing.T) {
	p := NewOpenRouterProvider()
	s := config.NewSession()
	s.ModelID = "openai/gpt-4o"
	s.DynamicSystemPrompt = "sp"
	s.ReasoningEffort = "none"
	if _, ok := p.buildBody("x", 600, s)["reasoning"]; ok {
		t.Errorf("non-reasoning model must not get a reasoning param")
	}
}

func TestParseChatCompletionKeepsUsageOnEmpty(t *testing.T) {
	raw := map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": ""},
			"finish_reason": "length",
		}},
		"usage": map[string]any{"prompt_tokens": float64(12), "completion_tokens": float64(600)},
	}
	res, err := parseChatCompletion(raw)
	if err != nil {
		t.Fatalf("empty content must not be a hard error: %v", err)
	}
	if !res.Truncated() {
		t.Errorf("finish_reason length must be reported as truncated")
	}
	if res.PromptTokens != 12 || res.CompletionTokens != 600 {
		t.Errorf("usage discarded: %+v", res)
	}
}

func TestContextBlockIncludedInSystem(t *testing.T) {
	p := NewOpenRouterProvider()
	s := config.NewSession()
	s.ModelID = "openai/gpt-4o"
	s.DynamicSystemPrompt = "BASE"
	s.ContextBlock = "CTX"

	body := p.buildBody("x", 600, s)
	msgs := body["messages"].([]map[string]any)
	sys := msgs[0]["content"].([]map[string]any)
	txt := sys[0]["text"].(string)
	if !strings.Contains(txt, "BASE") || !strings.Contains(txt, "CTX") {
		t.Errorf("system text = %q", txt)
	}
}

func TestRetryClassification(t *testing.T) {
	if !isRetryable(&HTTPError{Status: 429}) {
		t.Errorf("429 must be retryable")
	}
	if !isRetryable(&HTTPError{Status: 503}) {
		t.Errorf("503 must be retryable")
	}
	if isRetryable(&HTTPError{Status: 401}) {
		t.Errorf("401 must not be retryable")
	}
	if !isRetryable(errors.New("connection reset")) {
		t.Errorf("transport errors must be retryable")
	}
}

func TestOpenRouterRoutingBody(t *testing.T) {
	p := NewOpenRouterProvider()
	s := config.NewSession()
	s.DynamicSystemPrompt = "sp"
	s.ModelID = "openai/gpt-4o"
	s.ProviderSort = "price"
	s.AllowTraining = true

	body := p.buildBody("payload", 600, s)
	prov, ok := body["provider"].(map[string]any)
	if !ok {
		t.Fatalf("missing provider routing: %v", body)
	}
	if prov["sort"] != "price" {
		t.Errorf("sort = %v", prov["sort"])
	}
	if prov["data_collection"] != "allow" {
		t.Errorf("data_collection = %v", prov["data_collection"])
	}

	// Pinned provider takes precedence over sort.
	s.SelectedProvider = "DeepInfra"
	body = p.buildBody("payload", 600, s)
	prov = body["provider"].(map[string]any)
	if _, ok := prov["order"]; !ok {
		t.Errorf("expected provider order when pinned")
	}
	if prov["allow_fallbacks"] != true {
		t.Errorf("allow_fallbacks = %v", prov["allow_fallbacks"])
	}

	// "deny" when training is disallowed.
	s.AllowTraining = false
	s.SelectedProvider = ""
	prov = p.buildBody("payload", 600, s)["provider"].(map[string]any)
	if prov["data_collection"] != "deny" {
		t.Errorf("data_collection = %v", prov["data_collection"])
	}

	// Non-OpenRouter providers must not receive routing.
	oai := NewOpenAIProvider()
	if _, ok := oai.buildBody("payload", 600, s)["provider"]; ok {
		t.Errorf("OpenAI body must not contain routing")
	}
}
