package web

import (
	"net/http"
	"strings"
	"time"

	"translate_llm/internal/config"
)

// --- Models ----------------------------------------------------------------

func (s *Server) handleModels(w http.ResponseWriter, req *http.Request) {
	provider := sanitizeProvider(req.URL.Query().Get("provider"))
	cfg := config.LoadConfig()
	apiKey := config.GetAPIKey(provider, cfg)

	type pricing struct {
		Prompt         float64 `json:"prompt"`
		PromptCost     float64 `json:"prompt_cost"`
		Completion     float64 `json:"completion"`
		CompletionCost float64 `json:"completion_cost"`
		ContextLength  int     `json:"context_length"`
		Reasoning      bool    `json:"reasoning"`
	}
	models := []string{}
	pricingMap := map[string]pricing{}

	switch provider {
	case "OpenRouter":
		if data, err := s.openRouterModels(apiKey); err == nil {
			for _, item := range data {
				id, _ := item["id"].(string)
				if id == "" {
					continue
				}
				pr, _ := item["pricing"].(map[string]any)
				p := asFloat(pr["prompt"])
				c := asFloat(pr["completion"])
				ctx := asInt(item["context_length"])
				if ctx == 0 {
					ctx = config.DefaultContextLength
				}
				models = append(models, id)
				pricingMap[id] = pricing{p, p, c, c, ctx, supportsReasoning(id) || hasReasoningParam(item)}
			}
		}
		if len(models) == 0 {
			for _, id := range config.OpenRouterModelFallback {
				kc := config.KnownCosts[id]
				models = append(models, id)
				pricingMap[id] = pricing{kc[0], kc[0], kc[1], kc[1], config.DefaultContextLength, supportsReasoning(id)}
			}
		}
	case "OpenCode Zen", "OpenCode Go":
		models = s.openCodeModels(apiKey, provider == "OpenCode Zen")
		for _, id := range models {
			pricingMap[id] = pricing{0, 0, 0, 0, config.DefaultContextLength, supportsReasoning(id)}
		}
	default:
		models = config.ProviderModels[provider]
		for _, id := range models {
			kc := config.KnownCosts[id]
			pricingMap[id] = pricing{kc[0], kc[0], kc[1], kc[1], config.DefaultContextLength, supportsReasoning(id)}
		}
	}

	s.logger.Debug("models listed", "provider", provider, "count", len(models))
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": provider, "models": models, "pricing": pricingMap,
	})
}

func (s *Server) openRouterModels(apiKey string) ([]map[string]any, error) {
	s.modelsMu.Lock()
	if s.modelsCache != nil && time.Since(s.modelsTime) < 5*time.Minute {
		cached := s.modelsCache
		s.modelsMu.Unlock()
		return cached, nil
	}
	s.modelsMu.Unlock()

	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	data, err := httpGetJSON(config.OpenRouterModelsURL, headers, 5*time.Second)
	if err != nil {
		return nil, err
	}
	raw, _ := data["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	s.modelsMu.Lock()
	s.modelsCache = out
	s.modelsTime = time.Now()
	s.modelsMu.Unlock()
	return out, nil
}

func (s *Server) openCodeModels(apiKey string, zen bool) []string {
	base := config.OpenCodeGoBaseURL
	if zen {
		base = config.OpenCodeZenBaseURL
	}
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	if data, err := httpGetJSON(base+"/models", headers, 5*time.Second); err == nil {
		if raw, ok := data["data"].([]any); ok {
			var out []string
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok {
					if id, ok := m["id"].(string); ok && id != "" {
						out = append(out, id)
					}
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	} else {
		s.logger.Warn("OpenCode model catalogue fetch failed; using fallback", "zen", zen, "error", err)
	}
	return config.OpenCodeZenModels
}

func (s *Server) handleModelEndpoints(w http.ResponseWriter, req *http.Request) {
	modelID := req.URL.Query().Get("model_id")
	if modelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Missing model_id parameter."})
		return
	}
	data, err := httpGetJSON(config.OpenRouterEndpointsURL+modelID+"/endpoints", nil, 10*time.Second)
	if err != nil {
		s.logger.Warn("model endpoints fetch failed", "model", modelID, "error", err)
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "data": map[string]any{"endpoints": []any{}}})
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) getModelPricing(modelID string) (float64, float64, int) {
	kc, ok := config.KnownCosts[modelID]
	p, c := 0.0, 0.0
	if ok {
		p, c = kc[0], kc[1]
	}
	ctx := config.DefaultContextLength
	if (p == 0 && c == 0) || strings.HasPrefix(modelID, "openrouter/") || strings.Contains(modelID, "/") {
		if data, err := s.openRouterModels(""); err == nil {
			for _, item := range data {
				if id, _ := item["id"].(string); id == modelID {
					pr, _ := item["pricing"].(map[string]any)
					p = asFloat(pr["prompt"])
					c = asFloat(pr["completion"])
					if v := asInt(item["context_length"]); v > 0 {
						ctx = v
					}
					break
				}
			}
		}
	}
	return p, c, ctx
}

// reasoningMarkers identifies model families that support an effort/reasoning knob.
var reasoningMarkers = []string{
	"o1", "o3", "o4", "gpt-5", "deepseek-r1", "deepseek-reasoner", "deepseek-v4",
	"qwq", "qvq", "-think", "glm-4.6", "glm-5", "kimi-k2", "minimax-m",
	"claude-3-7", "claude-sonnet-4", "claude-opus-4", "gemini-2.5", "gemini-3",
}

func supportsReasoning(modelID string) bool {
	m := strings.ToLower(modelID)
	for _, k := range reasoningMarkers {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

func hasReasoningParam(item map[string]any) bool {
	params, ok := item["supported_parameters"].([]any)
	if !ok {
		return false
	}
	for _, p := range params {
		if s, ok := p.(string); ok {
			switch s {
			case "reasoning", "include_reasoning", "reasoning_effort":
				return true
			}
		}
	}
	return false
}
