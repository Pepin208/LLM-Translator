// Package engine contains the translation pipeline: token estimation, payload
// building, quality-controlled response parsing, batch processing and retries.
package engine

// Pair is a translatable tuple (id, text, name).
type Pair struct {
	ID   int
	Text string
	Name string
}

// BatchOutcome summarizes a processed batch.
type BatchOutcome struct {
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	PrimaryJSON      int
	JSONFallback     int
	LegacyFallback   int
	Recovered        int
	Status           string
}
