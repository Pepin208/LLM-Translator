// Endpoints and cross-cutting constants. Keeping every service URL and shared
// default here means provider, web and finance all read from one source instead
// of repeating string literals (which silently drift apart).
package config

import (
	"os"
	"strconv"
	"strings"
)

const (
	// Base URLs. Provider adapters append their protocol-specific path.
	OpenRouterBaseURL   = "https://openrouter.ai/api/v1"
	OpenAIBaseURL       = "https://api.openai.com/v1"
	DeepSeekBaseURL     = "https://api.deepseek.com/v1"
	AnthropicBaseURL    = "https://api.anthropic.com/v1"
	GeminiBaseURL       = "https://generativelanguage.googleapis.com/v1beta"
	OpenCodeZenBaseURL  = "https://opencode.ai/zen/v1"
	OpenCodeGoBaseURL   = "https://opencode.ai/zen/go/v1"
	LocalDefaultBaseURL = "http://localhost:11434/v1"

	// Fully-qualified endpoints shared by the web handlers and finance.
	OpenRouterChatURL      = OpenRouterBaseURL + "/chat/completions"
	OpenRouterCreditsURL   = OpenRouterBaseURL + "/credits"
	OpenRouterModelsURL    = OpenRouterBaseURL + "/models"
	OpenRouterEndpointsURL = OpenRouterBaseURL + "/models/"
	AnthropicMessagesURL   = AnthropicBaseURL + "/messages"
	CurrencyRatesURL       = "https://open.er-api.com/v6/latest/USD"

	// OpenRouterHost identifies OpenRouter traffic (referer/cache headers and
	// routing preferences are only valid there).
	OpenRouterHost = "openrouter.ai"

	// DefaultRefererURL and AppTitle are attribution headers sent to OpenRouter.
	DefaultRefererURL = "https://github.com/Pepin208/LLM-Translator"
	AppTitle          = "LLMT"

	// DefaultPort is the canonical LAN server port.
	DefaultPort = 21346

	// DefaultContextLength is used when a model's context window is unknown.
	DefaultContextLength = 128000
)

// RefererURL returns the OpenRouter attribution referer, overridable with
// $OPENROUTER_REFERER. A neutral project URL is the default so a public build
// does not advertise a personal domain.
func RefererURL() string {
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_REFERER")); v != "" {
		return v
	}
	return DefaultRefererURL
}

// ResolvePort returns the listen port, overridable with $PORT.
func ResolvePort() int {
	if v := strings.TrimSpace(os.Getenv("PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return DefaultPort
}
