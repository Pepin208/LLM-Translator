package providers

import "translate_llm/internal/config"

// OpenAIProvider talks to api.openai.com.
type OpenAIProvider struct{ openAICompatible }

// NewOpenAIProvider builds the OpenAI provider.
func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{openAICompatible{BaseURL: config.OpenAIBaseURL}}
}
