package providers

import "github.com/Pepin208/LLM-Translator/internal/config"

// MistralProvider talks to api.mistral.ai (OpenAI-compatible chat/completions).
type MistralProvider struct{ openAICompatible }

// NewMistralProvider builds the Mistral provider.
func NewMistralProvider() *MistralProvider {
	return &MistralProvider{openAICompatible{BaseURL: config.MistralBaseURL}}
}
