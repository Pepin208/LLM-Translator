package providers

import "translate_llm/internal/config"

// DeepSeekProvider talks to api.deepseek.com (OpenAI-compatible).
type DeepSeekProvider struct{ openAICompatible }

// NewDeepSeekProvider builds the DeepSeek provider.
func NewDeepSeekProvider() *DeepSeekProvider {
	return &DeepSeekProvider{openAICompatible{BaseURL: config.DeepSeekBaseURL}}
}
