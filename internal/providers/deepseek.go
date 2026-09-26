package providers

import "github.com/Pepin208/LLM-Translator/internal/config"

// DeepSeekProvider talks to api.deepseek.com (OpenAI-compatible).
type DeepSeekProvider struct{ openAICompatible }

// NewDeepSeekProvider builds the DeepSeek provider.
func NewDeepSeekProvider() *DeepSeekProvider {
	return &DeepSeekProvider{openAICompatible{BaseURL: config.DeepSeekBaseURL}}
}
