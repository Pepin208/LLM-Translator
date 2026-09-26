package engine

import (
	"regexp"
	"strings"
)

// --- Response cleanup ------------------------------------------------------

var (
	// Built with hex escapes so the literal "<think>"/"</think>" tags survive
	// any tooling that treats angle-bracket text as markup.
	thinkTag   = regexp.MustCompile("(?s)\x3cthink\x3e.*?(\x3c/think\x3e|$)")
	fenceOpen  = regexp.MustCompile("^```[A-Za-z]*\n")
	fenceClose = regexp.MustCompile("```$")
	bracketTag = regexp.MustCompile(`\[t(\d+)\]`)
	angleTag   = regexp.MustCompile(`<t\s*(\d+)\s*>`)
)

// CleanLLMResponse strips reasoning tags, markdown fences and normalizes tags.
func CleanLLMResponse(raw, modelID string) string {
	if strings.Contains(strings.ToLower(modelID), "deepseek") {
		raw = thinkTag.ReplaceAllString(raw, "")
	}
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		raw = fenceOpen.ReplaceAllString(raw, "")
		raw = fenceClose.ReplaceAllString(strings.TrimRight(raw, " \t\n"), "")
	}
	raw = bracketTag.ReplaceAllString(raw, "<t$1/>")
	raw = angleTag.ReplaceAllString(raw, "<t$1/>")
	return strings.TrimSpace(raw)
}
