package engine

import (
	"fmt"
	"strings"

	"translate_llm/internal/config"
)

// --- Token estimation ------------------------------------------------------

// EstimateTokens returns (estimatedPrompt, estimatedOutput).
func EstimateTokens(pairs []Pair, overlapLines []string, s *config.TranslationSession) (int, int) {
	var payloadLines []string
	for _, line := range overlapLines {
		payloadLines = append(payloadLines, "[C]: "+line)
	}
	type sanitized struct {
		id   int
		text string
		name string
	}
	sanitizedPairs := make([]sanitized, 0, len(pairs))
	for _, p := range pairs {
		st := SanitizeLineBreaks(p.Text)
		sanitizedPairs = append(sanitizedPairs, sanitized{p.ID, st, p.Name})
		if p.Name != "" {
			payloadLines = append(payloadLines, fmt.Sprintf("[%d] %s: %s", p.ID, p.Name, st))
		} else {
			payloadLines = append(payloadLines, fmt.Sprintf("[%d]: %s", p.ID, st))
		}
	}
	payloadString := strings.Join(payloadLines, "\n")

	totalInputChars := len(payloadString) + len(s.DynamicSystemPrompt) + len(s.ContextBlock)
	estimatedPrompt := int(float64(totalInputChars) / langFactor(s.SourceLang))

	pureTextChars := 0
	tagCount := 0
	for _, sp := range sanitizedPairs {
		tagCount += len(config.TagPattern.FindAllString(sp.text, -1))
		pure := config.TagPattern.ReplaceAllString(sp.text, "")
		pureTextChars += len(pure)
	}
	rawOutputEst := int(float64(pureTextChars) / langFactor(s.TargetLang))
	estimatedOutput := int(float64(rawOutputEst)*1.25) + tagCount*4 + len(pairs)*12 + 150
	if estimatedOutput < 600 {
		estimatedOutput = 600
	}
	return estimatedPrompt, estimatedOutput
}

func langFactor(lang string) float64 {
	if f, ok := config.LangCharFactors[lang]; ok {
		return f
	}
	return 4.0
}

// ValidateTokens returns whether the estimate fits and a safe max_tokens (>0).
func ValidateTokens(estimatedPrompt, estimatedOutput, contextLength int) (bool, int) {
	available := contextLength - estimatedPrompt - 250
	if available < 1 {
		available = 1
	}
	final := maxInt(estimatedOutput, 600)
	if final > available {
		final = available
	}
	if final < 1 {
		final = 1
	}
	isValid := estimatedPrompt+estimatedOutput <= contextLength-250
	return isValid, final
}
