package providers

import (
	"strings"

	"translate_llm/internal/config"
)

// LocalProvider talks to a user-supplied OpenAI-compatible server.
type LocalProvider struct {
	openAICompatible
}

// NewLocalProvider normalizes a user-entered base URL.
func NewLocalProvider(serverURL string) *LocalProvider {
	base := strings.TrimRight(serverURL, "/")
	switch {
	case strings.HasSuffix(base, "/v1/chat/completions"):
		base = strings.TrimSuffix(base, "/chat/completions")
	case strings.HasSuffix(base, "/v1"):
	case base == "":
		base = config.LocalDefaultBaseURL
	default:
		base += "/v1"
	}
	return &LocalProvider{openAICompatible{BaseURL: base}}
}
