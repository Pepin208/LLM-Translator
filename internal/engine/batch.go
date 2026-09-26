package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/providers"
	"github.com/Pepin208/LLM-Translator/internal/utils"
)

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
