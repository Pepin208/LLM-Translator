// Package engine contains the translation pipeline: token estimation, payload
// building, quality-controlled response parsing, batch processing and retries.
package engine

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"translate_llm/internal/config"
	"translate_llm/internal/providers"
	"translate_llm/internal/utils"
)

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

// --- Overlap context -------------------------------------------------------

// GetOverlapContext returns prior translated lines for sequential consistency.
// numLines < 0 selects the automatic policy.
func GetOverlapContext(currentStart int, dialogues []Pair, s *config.TranslationSession, numLines int) []string {
	if numLines < 0 {
		if currentStart < 3 {
			numLines = currentStart
		} else {
			end := currentStart + 10
			if end > len(dialogues) {
				end = len(dialogues)
			}
			sample := dialogues[currentStart:end]
			avg := 0.0
			if len(sample) > 0 {
				total := 0
				for _, d := range sample {
					total += len(d.Text)
				}
				avg = float64(total) / float64(len(sample))
			}
			if avg < 45 {
				numLines = 3
			} else {
				numLines = s.ContextLines
			}
		}
	}

	var contextLines []string
	for i := currentStart - 1; i >= 0; i-- {
		key := s.CacheKey(dialogues[i].ID, dialogues[i].Text)
		translated, ok := s.Cache[key]
		if !ok {
			continue
		}
		sanitized := SanitizeLineBreaks(translated)
		if name := dialogues[i].Name; name != "" {
			contextLines = append(contextLines, name+": "+sanitized)
		} else {
			contextLines = append(contextLines, sanitized)
		}
		if len(contextLines) == numLines {
			break
		}
	}
	// reverse
	for i, j := 0, len(contextLines)-1; i < j; i, j = i+1, j-1 {
		contextLines[i], contextLines[j] = contextLines[j], contextLines[i]
	}
	return contextLines
}

// --- Payload / response ----------------------------------------------------

func buildTranslationPayload(pairs []Pair, overlapLines []string, s *config.TranslationSession) string {
	var lines []string
	for _, text := range overlapLines {
		lines = append(lines, "[C]: "+text)
	}
	ids := map[int]struct{}{}
	for _, p := range pairs {
		sanitized := SanitizeLineBreaks(p.Text)
		if p.Name != "" {
			lines = append(lines, fmt.Sprintf("[%d] %s: %s", p.ID, p.Name, sanitized))
		} else {
			lines = append(lines, fmt.Sprintf("[%d]: %s", p.ID, sanitized))
		}
		ids[p.ID] = struct{}{}
	}
	// Hints are emitted in a deterministic order.
	hintIDs := make([]int, 0, len(s.RetryHints))
	for id := range s.RetryHints {
		if _, ok := ids[id]; ok {
			hintIDs = append(hintIDs, id)
		}
	}
	sort.Ints(hintIDs)
	for _, id := range hintIDs {
		lines = append(lines, "[HINT]: "+s.RetryHints[id])
	}
	return strings.Join(lines, "\n")
}

var (
	thinkTag   = regexp.MustCompile(`(?s)<think>.*?(</think>|$)`)
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

// --- Quality checks --------------------------------------------------------

var (
	nonWordSpace = regexp.MustCompile(`[^\p{L}\p{N}_\s]`)
	tagReplace   = regexp.MustCompile(`<t\d+/>|<br/>|<nbr/>`)
	digitsRe     = regexp.MustCompile(`\d+`)
)

func normForEcho(s string) string {
	s = tagReplace.ReplaceAllString(s, " ")
	s = nonWordSpace.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	return strings.TrimSpace(s)
}

func qualityCheckEcho(val, original string, s *config.TranslationSession, glossary []string) string {
	_, romanized := config.RomanizedSourceLangs[s.SourceLang]
	sourceIsLatin := !romanized

	normOrig := normForEcho(original)
	normVal := normForEcho(val)
	origWords := strings.Fields(normOrig)

	if !sourceIsLatin || len(origWords) < 2 || normOrig != normVal || !hasAlpha(normVal) {
		return ""
	}

	glossaryKeys := map[string]struct{}{}
	for _, n := range glossary {
		norm := utils.NormalizeNameKey(n)
		glossaryKeys[norm] = struct{}{}
		for _, w := range strings.Fields(norm) {
			glossaryKeys[w] = struct{}{}
		}
	}

	whitelisted := true
	for _, w := range origWords {
		if isAllDigitsStr(w) {
			continue
		}
		if _, ok := config.IdentityWhitelist[utils.NormalizeNameKey(w)]; ok {
			continue
		}
		if _, ok := glossaryKeys[utils.NormalizeNameKey(w)]; ok {
			continue
		}
		whitelisted = false
		break
	}
	if whitelisted {
		return ""
	}
	return fmt.Sprintf("The previous translation '%s' was not translated (echo of the source). Retranslate it into %s.", val, s.TargetLang)
}

// --- Request execution -----------------------------------------------------

// RequestStats captures parser-path counters.
type RequestStats struct {
	PromptTokens     int
	CompletionTokens int
	PrimaryJSON      int
	JSONFallback     int
	LegacyFallback   int
	// Truncated is set when the provider hit the max output token limit.
	Truncated bool
}

// ExecuteTranslationRequest performs one provider call and parses the response.
func ExecuteTranslationRequest(ctx context.Context, pairs []Pair, s *config.TranslationSession, maxTokens int, overlapLines []string) (map[int]string, RequestStats) {
	stats := RequestStats{}
	if len(pairs) == 0 {
		return map[int]string{}, stats
	}

	lookup := map[int]Pair{}
	for _, p := range pairs {
		lookup[p.ID] = p
	}

	payload := buildTranslationPayload(pairs, overlapLines, s)

	provider, err := ensureProvider(s)
	if err != nil {
		return map[int]string{}, stats
	}

	res, err := provider.GenerateCompletion(ctx, payload, maxTokens, s)
	if err != nil {
		if s.Out != nil {
			fmt.Fprintf(s.Out, "[API error] model=%s: %v\n", s.ModelID, err)
		}
		return map[int]string{}, stats
	}
	if res == nil {
		return map[int]string{}, stats
	}
	stats.PromptTokens = res.PromptTokens
	stats.CompletionTokens = res.CompletionTokens
	stats.Truncated = res.Truncated()
	if res.Truncated() && strings.TrimSpace(res.Content) == "" && s.Out != nil {
		fmt.Fprintf(s.Out, "[API] truncated empty response (finish_reason=%s); splitting\n", res.FinishReason)
	}
	s.LastCachedTokens = res.CachedTokens
	if res.CachedTokens > 0 {
		s.TotalCachedTokens += res.CachedTokens
	}

	translated := CleanLLMResponse(res.Content, s.ModelID)
	glossary := s.GlossaryNames

	result := map[int]string{}
	var fallbackLine string
	for _, line := range strings.Split(translated, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if fallbackLine == "" && !strings.HasPrefix(line, "[HINT]") && !strings.HasPrefix(line, "[C]") {
			fallbackLine = line
		}
		id, val, ok := parseDelimited(line, lookup)
		if !ok {
			continue
		}
		original := lookup[id].Text
		final, ok := finalizeTranslation(id, val, original, s, glossary)
		if !ok {
			continue
		}
		result[id] = final
	}

	// Fallback: a single-line retry may come back without the "ID|" prefix.
	if len(result) == 0 && len(pairs) == 1 && fallbackLine != "" {
		p := pairs[0]
		candidate := stripLeadingID(fallbackLine, p.ID)
		if final, ok := finalizeTranslation(p.ID, candidate, p.Text, s, glossary); ok {
			result[p.ID] = final
		}
	}

	if len(result) > 0 {
		stats.PrimaryJSON = 1
	}

	// Drop any keys the provider hallucinated outside the requested set.
	for id := range result {
		if _, ok := lookup[id]; !ok {
			delete(result, id)
		}
	}
	return result, stats
}

// finalizeTranslation applies sanitization and quality checks to a candidate,
// recording a retry hint when it is rejected.
func finalizeTranslation(id int, val, original string, s *config.TranslationSession, glossary []string) (string, bool) {
	val = sanitizeDelimitedValue(val)
	val = SanitizeOutputText(val, original, s.TargetLang)
	if len(glossary) > 0 {
		val = NormalizeGlossaryNames(val, glossary)
	}
	if val == "" && strings.TrimSpace(original) != "" {
		val = strings.TrimSpace(original)
	}
	if reason := qualityCheckEcho(val, original, s, glossary); reason != "" {
		s.RetryHints[id] = reason
		return "", false
	}
	if bad, reason := qualityCheckArtefacts(val, original, s); bad {
		s.RetryHints[id] = reason
		return "", false
	}
	if bad, reason := qualityCheckAnchors(val, original, glossary); bad {
		s.RetryHints[id] = reason
		return "", false
	}
	val = strings.ReplaceAll(val, "<br/>", `\N`)
	val = strings.ReplaceAll(val, "<nbr/>", `\n`)
	return val, true
}

// stripLeadingID removes an optional "ID|", "ID:" or leading dash from a
// fallback line so it can be used as the translation for the expected id.
func stripLeadingID(line string, id int) string {
	prefix := fmt.Sprintf("%d", id)
	l := strings.TrimSpace(line)
	if strings.HasPrefix(l, prefix) {
		rest := strings.TrimLeft(strings.TrimSpace(l[len(prefix):]), "|:-")
		return strings.TrimSpace(rest)
	}
	return l
}

func parseDelimited(line string, lookup map[int]Pair) (int, string, bool) {
	if idx := strings.Index(line, "|"); idx >= 0 {
		head := strings.TrimSpace(line[:idx])
		if isAllDigitsStr(head) {
			id := atoi(head)
			val := strings.TrimSpace(strings.Trim(strings.TrimSpace(line[idx+1:]), "|"))
			return id, val, true
		}
	}
	m := idLineRe.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	id := atoi(m[1])
	rawBody := strings.TrimSpace(strings.Trim(strings.TrimSpace(m[2]), "|"))
	if p, ok := lookup[id]; ok && p.Name != "" && strings.HasPrefix(strings.ToLower(rawBody), strings.ToLower(p.Name)+":") {
		rawBody = strings.TrimSpace(rawBody[len(p.Name)+1:])
	}
	return id, rawBody, true
}

var idLineRe = regexp.MustCompile(`^\[?(\d+)\]?:?\s*(.*)$`)

func sanitizeDelimitedValue(val string) string {
	val = strings.TrimSpace(strings.Trim(val, "|"))
	val = strings.TrimSpace(val)
	val = dashFix1.ReplaceAllString(val, "- ")
	val = dashFix2.ReplaceAllString(val, "<br/>- ")
	val = dashFix3.ReplaceAllString(val, "- ")
	val = dashFix4.ReplaceAllString(val, "$1- ")
	return val
}

var (
	dashFix1 = regexp.MustCompile(`^(?:<br/>|<nbr/>)?\s*\|\-\|\s*\-?\s*`)
	dashFix2 = regexp.MustCompile(`(?:<br/>|<nbr/>)\s*\|\-\|\s*\-?\s*`)
	dashFix3 = regexp.MustCompile(`^\-\s*`)
	dashFix4 = regexp.MustCompile(`(<br/>|<nbr/>)\s*\-\s*`)
)

func qualityCheckArtefacts(val, original string, s *config.TranslationSession) (bool, string) {
	valPlain := strings.ReplaceAll(strings.ReplaceAll(val, "<br/>", " "), "<nbr/>", " ")
	lowered := strings.ToLower(valPlain)
	if s.SourceLang == "EN" {
		for _, idiom := range config.EchoIdioms {
			if strings.Contains(lowered, idiom) {
				return true, fmt.Sprintf("The previous translation '%s' was a literal calque of English. Use a natural equivalent Spanish expression.", val)
			}
		}
	}
	if strings.Count(val, "{") != strings.Count(val, "}") {
		return true, fmt.Sprintf("The previous translation '%s' has unbalanced braces. Preserve the exact tags and placeholders.", val)
	}
	if len(val) < 2 && len(original) > 3 {
		return true, fmt.Sprintf("The previous translation '%s' was truncated. Translate the full line.", val)
	}
	return false, ""
}

var numberWordMap = map[string][]string{
	"1": {"1", "uno", "una", "primero", "primera", "número", "nº", "n°"},
	"2": {"2", "dos", "segundo", "segunda", "número", "nº", "n°"},
	"3": {"3", "tres", "tercero", "tercera", "número", "nº", "n°"},
	"4": {"4", "cuatro", "cuarto", "cuarta"},
	"5": {"5", "cinco", "quinto", "quinta"},
}

func qualityCheckAnchors(val, original string, glossary []string) (bool, string) {
	srcPlain := tagReplace.ReplaceAllString(original, " ")
	srcNK := utils.NormalizeNameKey(srcPlain)

	anchors := map[string]struct{}{}
	for _, d := range digitsRe.FindAllString(srcNK, -1) {
		anchors[d] = struct{}{}
	}
	for _, g := range glossary {
		ng := utils.NormalizeNameKey(g)
		if ng != "" && strings.Contains(srcNK, ng) {
			anchors[ng] = struct{}{}
		}
	}
	if len(anchors) == 0 {
		return false, ""
	}

	valNK := utils.NormalizeNameKey(strings.ReplaceAll(strings.ReplaceAll(val, "<br/>", " "), "<nbr/>", " "))
	for a := range anchors {
		if strings.Contains(valNK, a) {
			return false, ""
		}
		if words, ok := numberWordMap[a]; ok {
			for _, w := range words {
				if strings.Contains(valNK, w) {
					return false, ""
				}
			}
		}
	}
	keys := make([]string, 0, len(anchors))
	for a := range anchors {
		keys = append(keys, a)
	}
	sort.Strings(keys)
	return true, fmt.Sprintf("The previous translation '%s' does not correspond to this line. Keep every number and proper name exactly; translate only the text of this exact line.", val)
}

// --- Batch processing ------------------------------------------------------

func ensureProvider(s *config.TranslationSession) (providers.Provider, error) {
	if s.ProviderInstance != nil {
		if p, ok := s.ProviderInstance.(providers.Provider); ok {
			return p, nil
		}
	}
	name := s.ActiveProvider
	if name == "" {
		name = "OpenRouter"
	}
	p, err := providers.CreateProvider(name, s.LocalServerURL)
	if err != nil {
		return nil, err
	}
	s.ProviderInstance = p
	return p, nil
}

// maxSplitDepth bounds the adaptive splitting to avoid call explosion.
const maxSplitDepth = 3

// ProcessTranslationBatch runs one API call, updates the cache and reports status.
// When the provider truncates, only the lines still missing are retried (the
// partial results of the truncated attempt are kept), split in half up to
// maxSplitDepth levels.
func ProcessTranslationBatch(ctx context.Context, pairs []Pair, s *config.TranslationSession, maxTokens int, overlapLines []string) BatchOutcome {
	return processBatch(ctx, pairs, s, maxTokens, overlapLines, 0)
}

func processBatch(ctx context.Context, pairs []Pair, s *config.TranslationSession, maxTokens int, overlapLines []string, depth int) BatchOutcome {
	results, stats := ExecuteTranslationRequest(ctx, pairs, s, maxTokens, overlapLines)

	out := BatchOutcome{
		PromptTokens:     stats.PromptTokens,
		CompletionTokens: stats.CompletionTokens,
		Cost:             float64(stats.PromptTokens)*s.PromptCost + float64(stats.CompletionTokens)*s.CompletionCost,
		PrimaryJSON:      stats.PrimaryJSON,
		JSONFallback:     stats.JSONFallback,
		LegacyFallback:   stats.LegacyFallback,
	}

	// Keep partial results; only retry what is genuinely missing.
	var missing []Pair
	for _, p := range pairs {
		if text, ok := results[p.ID]; ok {
			s.Cache[s.CacheKey(p.ID, p.Text)] = text
			out.Recovered++
		} else {
			missing = append(missing, p)
		}
	}

	if stats.Truncated && len(missing) > 1 && depth < maxSplitDepth {
		mid := len(missing) / 2
		left := processBatch(ctx, missing[:mid], s, maxTokens, overlapLines, depth+1)
		right := processBatch(ctx, missing[mid:], s, maxTokens, overlapLines, depth+1)
		out.PromptTokens += left.PromptTokens + right.PromptTokens
		out.CompletionTokens += left.CompletionTokens + right.CompletionTokens
		out.Cost += left.Cost + right.Cost
		out.PrimaryJSON += left.PrimaryJSON + right.PrimaryJSON
		out.JSONFallback += left.JSONFallback + right.JSONFallback
		out.LegacyFallback += left.LegacyFallback + right.LegacyFallback
		out.Recovered += left.Recovered + right.Recovered
		out.Status = "OK_SPLIT"
		saveCacheThrottled(s)
		return out
	}

	saveCacheThrottled(s)
	switch {
	case len(results) == 0:
		out.Status = "FAILED"
	case len(missing) > 0:
		out.Status = fmt.Sprintf("OK_MISSING_%d", len(missing))
	default:
		out.Status = "OK"
	}
	return out
}

func saveCacheThrottled(s *config.TranslationSession) {
	s.CacheWriteCounter++
	if s.CacheWriteCounter == 1 || s.CacheWriteCounter%config.CacheSaveInterval == 0 {
		_ = config.SaveCache(s.Cache, s.CacheFilepath)
	}
}

// GetValidSubBatch binary-searches the largest batch that fits the context window.
func GetValidSubBatch(currentIdx, endIdx int, pairs []Pair, dialogues []Pair, s *config.TranslationSession) (int, int) {
	low, high := 1, len(pairs)
	bestSize := 0
	bestMaxTokens := 500

	overlap := GetOverlapContext(currentIdx, dialogues, s, -1)
	for low <= high {
		mid := (low + high) / 2
		estPrompt, estOutput := EstimateTokens(pairs[:mid], overlap, s)
		valid, finalMax := ValidateTokens(estPrompt, estOutput, s.ContextLength)
		if valid {
			bestSize = mid
			bestMaxTokens = finalMax
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return bestSize, bestMaxTokens
}

// RetryOutcome summarizes a retry sweep.
type RetryOutcome struct {
	PromptTokens     int
	CompletionTokens int
	Cost             float64
	PrimaryJSON      int
	JSONFallback     int
	LegacyFallback   int
	Recovered        int
}

// RunRetryLoop retries contiguous groups of missing lines.
func RunRetryLoop(ctx context.Context, missingIndices []int, dialogues []Pair, s *config.TranslationSession) RetryOutcome {
	var out RetryOutcome
	groups := utils.GroupContiguousIndices(missingIndices)

	var batches [][]int
	for _, group := range groups {
		for i := 0; i < len(group); i += config.MaxRetryBatch {
			end := i + config.MaxRetryBatch
			if end > len(group) {
				end = len(group)
			}
			batches = append(batches, group[i:end])
		}
	}

	for _, batchIdx := range batches {
		pairs := make([]Pair, 0, len(batchIdx))
		for _, i := range batchIdx {
			pairs = append(pairs, dialogues[i])
		}
		start := batchIdx[0]
		overlap := GetOverlapContext(start, dialogues, s, config.FallbackContextLines)
		estPrompt, estOutput := EstimateTokens(pairs, overlap, s)
		_, maxTokens := ValidateTokens(estPrompt, estOutput, s.ContextLength)

		outcome := ProcessTranslationBatch(ctx, pairs, s, maxTokens, overlap)
		out.PromptTokens += outcome.PromptTokens
		out.CompletionTokens += outcome.CompletionTokens
		out.Cost += outcome.Cost
		out.PrimaryJSON += outcome.PrimaryJSON
		out.JSONFallback += outcome.JSONFallback
		out.LegacyFallback += outcome.LegacyFallback
		out.Recovered += outcome.Recovered

		// Second pass for a stubborn single line: drop the global context and
		// glossary, which often cause over-preservation or echo of the source.
		if outcome.Recovered == 0 && len(pairs) == 1 {
			tmp := *s
			tmp.ContextBlock = ""
			tmp.GlossaryNames = nil
			tmp.DynamicSystemPrompt = config.BuildSystemPrompt(s.SourceLang, s.TargetLang, s.ModelID, "")
			retry := ProcessTranslationBatch(ctx, pairs, &tmp, maxTokens, nil)
			out.PromptTokens += retry.PromptTokens
			out.CompletionTokens += retry.CompletionTokens
			out.Cost += retry.Cost
			out.Recovered += retry.Recovered
		}
	}
	return out
}

// --- helpers ---------------------------------------------------------------

func hasAlpha(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func isAllDigitsStr(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
