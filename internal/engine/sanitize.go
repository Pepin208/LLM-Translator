package engine

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/Pepin208/LLM-Translator/internal/utils"
)

// --- Text sanitization -----------------------------------------------------

// SanitizeLineBreaks converts literal/escaped breaks into XML tags.
func SanitizeLineBreaks(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "<nbr/>")
	text = strings.ReplaceAll(text, "\n", "<nbr/>")
	text = strings.ReplaceAll(text, `\N`, "<br/>")
	text = strings.ReplaceAll(text, `\n`, "<nbr/>")
	return text
}

var (
	multiQuestion = regexp.MustCompile(`¿{2,}`)
	multiExclaim  = regexp.MustCompile(`¡{2,}`)
	multiQMark    = regexp.MustCompile(`\?{2,}`)
	multiEMark    = regexp.MustCompile(`!{2,}`)
	spacePunct    = regexp.MustCompile(`\s+([,.;:?!])`)
	multiSpaceOut = regexp.MustCompile(` {2,}`)
	multiDot      = regexp.MustCompile(`\.{4,}`)
	dotSpaceDot   = regexp.MustCompile(`\.\s+\.`)
)

// SanitizeOutputText applies deterministic punctuation cleanup to a translation.
func SanitizeOutputText(text, originalText, targetLang string) string {
	if text == "" {
		return text
	}
	text = multiQuestion.ReplaceAllString(text, "¿")
	text = multiExclaim.ReplaceAllString(text, "¡")
	text = multiQMark.ReplaceAllString(text, "?")
	text = multiEMark.ReplaceAllString(text, "!")
	text = strings.ReplaceAll(text, "?!", "?")
	text = strings.ReplaceAll(text, "!?", "!")
	text = strings.ReplaceAll(text, "?.", "?")
	text = strings.ReplaceAll(text, "!.", "!")
	text = spacePunct.ReplaceAllString(text, "$1")
	text = multiSpaceOut.ReplaceAllString(text, " ")
	text = multiDot.ReplaceAllString(text, "...")
	text = dotSpaceDot.ReplaceAllString(text, ".")

	if originalText != "" && (strings.Contains(originalText, "<br/>") || strings.Contains(originalText, `\N`)) {
		if !strings.Contains(text, "<br/>") && !strings.Contains(text, `\N`) {
			words := strings.Split(text, " ")
			if len(words) > 1 {
				mid := len(words) / 2
				text = strings.Join(words[:mid], " ") + "<br/>" + strings.Join(words[mid:], " ")
			}
		}
	}

	if strings.HasPrefix(strings.ToUpper(targetLang), "ES") {
		text = injectOpening(text, "¿", "?")
		text = injectOpening(text, "¡", "!")
	}
	return strings.TrimSpace(text)
}

var leadingTags = regexp.MustCompile(`^(?:\s*<[^>]+>)*\s*`)

func injectOpening(s, mark, closing string) string {
	if strings.Contains(s, closing) && !strings.Contains(s, mark) {
		loc := leadingTags.FindStringIndex(s)
		prefixLen := 0
		if loc != nil {
			prefixLen = loc[1]
		}
		return s[:prefixLen] + mark + s[prefixLen:]
	}
	return s
}

// NormalizeGlossaryNames rewrites glossary-name variants to canonical spelling.
// Python used lookbehind/lookahead which RE2 lacks, so boundaries are checked
// manually with Unicode-aware rune tests.
func NormalizeGlossaryNames(text string, glossary []string) string {
	if text == "" || len(glossary) == 0 {
		return text
	}
	for _, name := range glossary {
		key := utils.NormalizeNameKey(name)
		if key == "" {
			continue
		}
		text = replaceWordBoundary(text, key, name)
	}
	return text
}

func replaceWordBoundary(text, key, replacement string) string {
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(key))
	runes := []rune(text)
	var b strings.Builder
	last := 0
	for _, loc := range re.FindAllStringIndex(text, -1) {
		// Map byte offsets to rune indices around the match.
		startRune := byteToRuneIndex(text, loc[0])
		endRune := byteToRuneIndex(text, loc[1])
		leftOK := startRune == 0 || !isWordRune(runes[startRune-1])
		rightOK := endRune >= len(runes) || !isWordRune(runes[endRune])
		if leftOK && rightOK {
			b.WriteString(text[last:loc[0]])
			b.WriteString(replacement)
			last = loc[1]
		}
	}
	b.WriteString(text[last:])
	return b.String()
}

func byteToRuneIndex(s string, bytePos int) int {
	return len([]rune(s[:bytePos]))
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
