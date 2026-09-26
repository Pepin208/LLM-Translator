package providers

import "github.com/Pepin208/LLM-Translator/internal/config"

// OpenRouterProvider talks to openrouter.ai (OpenAI-compatible).
type OpenRouterProvider struct{ openAICompatible }

// NewOpenRouterProvider builds the OpenRouter provider.
func NewOpenRouterProvider() *OpenRouterProvider {
	return &OpenRouterProvider{openAICompatible{
		BaseURL:         config.OpenRouterBaseURL,
		useCacheControl: true,
		reasoningMarkers: []string{
			"deepseek-r1", "deepseek-reasoner", "v4-flash", "-think", "qwq",
			"qvq", "o1-", "/o1", "o3-", "/o3", "r1-",
		},
	}}
}
