package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"translate_llm/internal/config"
	"translate_llm/internal/finance"
	"translate_llm/internal/parser"
	"translate_llm/internal/subtitle"
)

// QueueItem is one file scheduled for translation.
type QueueItem struct {
	Filepath      string
	Ext           string
	BaseDir       string
	BaseName      string
	Dialogues     []parser.Dialogue
	TagMaps       map[int][]string
	Subtitle      *subtitle.File
	CacheFilepath string
}

func toPairs(dialogues []parser.Dialogue) []Pair {
	pairs := make([]Pair, len(dialogues))
	for i, d := range dialogues {
		pairs[i] = Pair{ID: d.ID, Text: d.Text, Name: d.Name}
	}
	return pairs
}

// StripEpisodeSuffix removes a trailing episode token from a basename.
var episodeSuffix = regexp.MustCompile(`(?:[\s_.\-]+(?:EP?|S\d+E)?\d{1,3}(?:\.\d)?)\s*$`)

func StripEpisodeSuffix(name string) string {
	return strings.TrimSpace(episodeSuffix.ReplaceAllString(name, ""))
}

// DetectSeriesName infers the series name from subtitle basenames.
func DetectSeriesName(targetFiles []string) string {
	if len(targetFiles) == 0 {
		return ""
	}
	baseNames := make([]string, len(targetFiles))
	for i, f := range targetFiles {
		base := filepath.Base(f)
		baseNames[i] = strings.TrimSuffix(base, filepath.Ext(base))
	}

	var cleaned string
	if len(baseNames) >= 2 {
		prefix := commonPrefix(baseNames)
		cleaned = strings.TrimRight(strings.TrimSpace(prefix), "0123456789 _-.,()[]")
	} else {
		cleaned = StripEpisodeSuffix(baseNames[0])
	}
	if len(cleaned) < 3 {
		return ""
	}
	return cleaned
}

func commonPrefix(items []string) string {
	if len(items) == 0 {
		return ""
	}
	prefix := items[0]
	for _, s := range items[1:] {
		i := 0
		for i < len(prefix) && i < len(s) && prefix[i] == s[i] {
			i++
		}
		prefix = prefix[:i]
	}
	return prefix
}

func mergeGlossaryOverrides(seriesName string, names []string) []string {
	override := config.GlossaryOverrides[seriesName]
	if len(override) == 0 {
		return names
	}
	merged := append([]string{}, override...)
	seen := map[string]struct{}{}
	for _, n := range merged {
		seen[normalizeKey(n)] = struct{}{}
	}
	for _, name := range names {
		k := normalizeKey(name)
		if _, ok := seen[k]; !ok {
			merged = append(merged, name)
			seen[k] = struct{}{}
		}
	}
	if len(merged) > 20 {
		merged = merged[:20]
	}
	return merged
}

// LoadOrBuildGlossary loads the series glossary from disk or builds and caches it.
func LoadOrBuildGlossary(seriesName string, parsedFiles []*subtitle.File, baseDir string) []string {
	if seriesName == "" {
		return nil
	}
	glossaryPath := filepath.Join(baseDir, fmt.Sprintf(".%s_glossary.json", seriesName))

	if data, err := os.ReadFile(glossaryPath); err == nil {
		var cached struct {
			Serie string   `json:"serie"`
			Names []string `json:"names"`
		}
		if json.Unmarshal(data, &cached) == nil && cached.Serie == seriesName {
			return mergeGlossaryOverrides(seriesName, cached.Names)
		}
	}

	counter := map[string]int{}
	var order []string
	for _, f := range parsedFiles {
		for _, name := range parser.ExtractProperNames(f) {
			if _, ok := counter[name]; !ok {
				order = append(order, name)
			}
			counter[name]++
		}
	}
	type mc struct {
		name  string
		count int
	}
	items := make([]mc, 0, len(counter))
	for _, n := range order {
		items = append(items, mc{n, counter[n]})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].count > items[j].count })
	names := []string{}
	for i, it := range items {
		if i >= 20 {
			break
		}
		names = append(names, it.name)
	}
	names = mergeGlossaryOverrides(seriesName, names)

	if len(names) > 0 {
		payload := map[string]any{"serie": seriesName, "names": names}
		if data, err := json.MarshalIndent(payload, "", "  "); err == nil {
			_ = os.WriteFile(glossaryPath, data, 0o644)
		}
	}
	return names
}

// BuildExecutionQueue parses every file, computes cost projections and detects
// the series glossary.
func BuildExecutionQueue(
	targetFiles []string,
	excludedRanges [][2]int,
	s *config.TranslationSession,
	exchangeRate *decimal.Decimal,
	targetCurrency string,
) ([]QueueItem, float64, float64) {
	var (
		queue       []QueueItem
		parsedFiles []*subtitle.File
		totalIdeal  float64
		totalWorst  float64
	)

	for _, filePath := range targetFiles {
		ext := strings.ToLower(filepathExt(filePath))
		baseDir := filepath.Dir(filePath)
		baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

		f, err := subtitle.Parse(filePath)
		if err != nil {
			fmt.Fprintf(s.Out, "Error parsing subtitle file '%s': %v\n", filePath, err)
			continue
		}
		dialogues, tagMaps := parser.ExtractSubtitles(f, excludedRanges)
		if len(dialogues) == 0 {
			fmt.Fprintf(s.Out, "No dialogue lines found to translate in '%s'.\n", filePath)
			continue
		}
		parsedFiles = append(parsedFiles, f)

		totalLines := len(dialogues)
		totalChars := 0
		for _, d := range dialogues {
			totalChars += len(d.Text)
		}
		avgChars := 0.0
		if totalLines > 0 {
			avgChars = float64(totalChars) / float64(totalLines)
		}

		sysTokens := float64(len(s.DynamicSystemPrompt)) / 4.0
		srcFactor := factorFor(s.SourceLang)
		tgtFactor := factorFor(s.TargetLang)

		promptBaseTokens := float64(totalChars) / srcFactor
		projBatch := float64(effectiveBatchSize(s))
		projContextLines := s.ContextLines
		if projContextLines < 0 {
			projContextLines = config.ContextLines
		}
		expectedBatches := math.Ceil(float64(totalLines) / projBatch)
		contextAdditions := expectedBatches * float64(projContextLines) * avgChars / srcFactor
		sysPromptAdditions := expectedBatches * sysTokens

		idealPromptTokens := promptBaseTokens + contextAdditions + sysPromptAdditions
		idealCompletionTokens := (float64(totalChars)/tgtFactor)*1.27 + float64(totalLines)*4.0

		cIdeal := idealPromptTokens*s.PromptCost + idealCompletionTokens*s.CompletionCost

		retryBatches := math.Ceil(float64(totalLines) / float64(config.MaxRetryBatch))
		retryContext := retryBatches * float64(config.FallbackContextLines) * avgChars / srcFactor
		retrySys := retryBatches * sysTokens
		retryPromptTokens := promptBaseTokens + retryContext + retrySys
		retryCost := retryPromptTokens*s.PromptCost + idealCompletionTokens*s.CompletionCost

		fallbackContext := float64(totalLines) * float64(config.FallbackContextLines) * avgChars / srcFactor
		fallbackSys := float64(totalLines) * sysTokens
		fallbackPromptTokens := promptBaseTokens + fallbackContext + fallbackSys
		fallbackCost := fallbackPromptTokens*s.PromptCost + idealCompletionTokens*s.CompletionCost

		cWorst := cIdeal + retryCost + fallbackCost
		totalIdeal += cIdeal
		totalWorst += cWorst

		finance.DisplayFinancialProjection(filePath, cIdeal, cWorst, exchangeRate, targetCurrency, s.Out)

		cacheFilepath := filepath.Join(baseDir, fmt.Sprintf(".%s_cache.json", baseName))
		queue = append(queue, QueueItem{
			Filepath:      filePath,
			Ext:           ext,
			BaseDir:       baseDir,
			BaseName:      baseName,
			Dialogues:     dialogues,
			TagMaps:       tagMaps,
			Subtitle:      f,
			CacheFilepath: cacheFilepath,
		})
	}

	seriesName := resolveSeriesName(targetFiles)
	episodeKeys := make([]string, 0, len(targetFiles))
	for _, f := range targetFiles {
		episodeKeys = append(episodeKeys, EpisodeKey(f))
	}
	glossary := BuildSeriesGlossary(context.Background(), seriesName, parsedFiles, episodeKeys, s)
	if glossary != nil {
		s.GlossaryNames = glossary.Names
		if block := formatGlossaryForPrompt(glossary); block != "" {
			s.DynamicSystemPrompt += "\n" + block
			fmt.Fprintf(s.Out, "\n[Glossary Active] Serie: '%s' | %d nombres, %d lugares, %d términos\n",
				seriesName, len(glossary.Names), len(glossary.Locations), len(glossary.Terms))
		}
	}

	return queue, totalIdeal, totalWorst
}

// resolveSeriesName picks a single episode-independent series key.
func resolveSeriesName(targetFiles []string) string {
	if len(targetFiles) == 0 {
		return ""
	}
	first := SeriesKey(targetFiles[0])
	for _, f := range targetFiles[1:] {
		if SeriesKey(f) != first {
			return DetectSeriesName(targetFiles)
		}
	}
	return first
}

// ExecuteTranslationQueue translates every queued file.
func ExecuteTranslationQueue(
	ctx context.Context,
	queue []QueueItem,
	s *config.TranslationSession,
	exchangeRate *decimal.Decimal,
	targetCurrency string,
	totalFilesCount int,
	outputDirOverride string,
) {
	var (
		statPrimary, statJSON, statLegacy int
		batchPrompt, batchCompletion      int
		batchCost                         float64
	)

	for _, item := range queue {
		select {
		case <-ctx.Done():
			fmt.Fprintln(s.Out, "\n[CANCEL] Translation cancelled by user. Stopping queue execution.")
			goto summary
		default:
		}

		s.CacheFilepath = item.CacheFilepath
		s.Cache = config.LoadCache(item.CacheFilepath)
		s.RetryHints = map[int]string{}
		s.CacheWriteCounter = 0

		fmt.Fprintf(s.Out, "\nStarting translation for '%s' (%d dialogue lines)...\n",
			filepath.Base(item.Filepath), len(item.Dialogues))

		dialogues := toPairs(item.Dialogues)
		translated := map[int]string{}
		totalValid := len(dialogues)

		s.ContextBlock = buildContextBlock(ctx, item, s)
		if s.ContextBlock != "" {
			fmt.Fprintf(s.Out, "[Context] mode='%s' injected (%d chars)\n", s.ContextMode, len(s.ContextBlock))
		}

		for i := 0; i < totalValid; {
			select {
			case <-ctx.Done():
				fmt.Fprintf(s.Out, "\n[CANCEL] Translation cancelled for '%s'.\n", filepath.Base(item.Filepath))
				goto finalize
			default:
			}

			endIdx := i + effectiveBatchSize(s)
			if endIdx > totalValid {
				endIdx = totalValid
			}
			s.RetryHints = map[int]string{}

			window := dialogues[i:endIdx]
			bestSize, maxTokens := GetValidSubBatch(i, endIdx, window, dialogues, s)
			if bestSize < 1 {
				bestSize = 1
				maxTokens = 500
			}
			actual := window[:bestSize]
			// With global context (briefing/full) the overlap window is redundant.
			var overlap []string
			if s.ContextMode == "" || s.ContextMode == "overlap" {
				overlap = GetOverlapContext(i, dialogues, s, -1)
			}

			outcome := ProcessTranslationBatch(ctx, actual, s, maxTokens, overlap)
			batchPrompt += outcome.PromptTokens
			batchCompletion += outcome.CompletionTokens
			batchCost += outcome.Cost
			if outcome.PrimaryJSON > 0 {
				statPrimary++
			} else if outcome.JSONFallback > 0 {
				statJSON++
			} else if outcome.LegacyFallback > 0 {
				statLegacy++
			}

			var missing []int
			for idx, p := range actual {
				if _, ok := s.Cache[s.CacheKey(p.ID, p.Text)]; !ok {
					missing = append(missing, i+idx)
				}
			}
			if len(missing) > 0 {
				fmt.Fprintf(s.Out, "\nBatch at index %d has %d missing/rejected lines. Running retry loop...\n", i, len(missing))
				r := RunRetryLoop(ctx, missing, dialogues, s)
				batchPrompt += r.PromptTokens
				batchCompletion += r.CompletionTokens
				batchCost += r.Cost
				if r.JSONFallback > 0 {
					statJSON++
				} else if r.LegacyFallback > 0 {
					statLegacy++
				}
			}

			for _, p := range actual {
				if text, ok := s.Cache[s.CacheKey(p.ID, p.Text)]; ok {
					translated[p.ID] = text
				} else {
					translated[p.ID] = p.Text
					// Interjections and proper names legitimately stay identical.
					if s.SourceLang == "EN" && qualityCheckEcho(p.Text, p.Text, s, s.GlossaryNames) == "" {
						fmt.Fprintf(s.Out, "• Line %d kept as-is (interjection/name).\n", p.ID)
					} else {
						fmt.Fprintf(s.Out, "⚠️ Line %d could not be translated after retries. Preserving original text.\n", p.ID)
					}
				}
			}

			i += bestSize
			fmt.Fprintf(s.Out, "Progress for '%s': %d/%d lines (%.1f%%) [%s]\n",
				filepath.Base(item.Filepath), i, totalValid, float64(i)/float64(totalValid)*100, outcome.Status)
		}

	finalize:
		_ = config.SaveCache(s.Cache, item.CacheFilepath)

		langSuffix := strings.ToLower(s.TargetLang)
		if len(langSuffix) > 2 {
			langSuffix = langSuffix[:2]
		}
		outDir := item.BaseDir
		if outputDirOverride != "" {
			outDir = outputDirOverride
		}
		outFilepath := filepath.Join(outDir, fmt.Sprintf("%s_%s%s", item.BaseName, langSuffix, item.Ext))

		fmt.Fprintf(s.Out, "\nFinalizing file reconstruction for '%s'...\n", filepath.Base(item.Filepath))
		if err := parser.Reconstruct(item.Subtitle, translated, item.TagMaps, outFilepath); err != nil {
			fmt.Fprintf(s.Out, "[ERROR] Failed to write '%s': %v\n", outFilepath, err)
			continue
		}
		fmt.Fprintf(s.Out, "✅ Successfully saved translated file to: %s\n", outFilepath)

		if _, err := os.Stat(item.CacheFilepath); err == nil {
			_ = os.Remove(item.CacheFilepath)
			fmt.Fprintf(s.Out, "Cleaned up cache file: %s\n", item.CacheFilepath)
		}
	}

summary:
	finance.PrintBatchSummary(
		totalFilesCount, statPrimary, statJSON, statLegacy,
		batchPrompt, batchCompletion, batchCost,
		&s.InitialUSDBalance, exchangeRate, targetCurrency, s, s.Out,
	)
}

// --- helpers ---------------------------------------------------------------

func filepathExt(p string) string {
	return filepath.Ext(p)
}

func factorFor(lang string) float64 {
	if f, ok := config.LangCharFactors[lang]; ok {
		return f
	}
	return 4.0
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func effectiveBatchSize(s *config.TranslationSession) int {
	if s.BatchSize > 0 {
		return s.BatchSize
	}
	return config.ChunkSize
}

// buildContextBlock returns the per-file stable context block according to the
// configured context mode ("" for plain overlap mode).
func buildContextBlock(ctx context.Context, item QueueItem, s *config.TranslationSession) string {
	switch strings.ToLower(s.ContextMode) {
	case "full":
		return fullContextBlock(item, s)
	case "briefing":
		return briefingContextBlock(ctx, item, s)
	default:
		return ""
	}
}

func fullContextBlock(item QueueItem, s *config.TranslationSession) string {
	var b strings.Builder
	for _, d := range item.Dialogues {
		fmt.Fprintf(&b, "[%d] %s\n", d.ID, SanitizeLineBreaks(d.Text))
	}
	block := "- FULL SUBTITLE CONTEXT (reference only; translate ONLY the requested lines):\n" + b.String()
	// Degrade to overlap when the block would not leave room for output.
	reserve := 8000
	if s.ContextLength > 0 && len(block)/4 > s.ContextLength-reserve {
		fmt.Fprintf(s.Out, "[Context] full context too large (~%d tokens); degrading\n", len(block)/4)
		return ""
	}
	return block
}

func briefingContextBlock(ctx context.Context, item QueueItem, s *config.TranslationSession) string {
	path := filepath.Join(item.BaseDir, fmt.Sprintf(".%s_briefing.json", item.BaseName))
	if data, err := os.ReadFile(path); err == nil {
		var cached struct {
			Briefing string `json:"briefing"`
		}
		if json.Unmarshal(data, &cached) == nil && cached.Briefing != "" {
			return "- SUBTITLE BRIEFING:\n" + cached.Briefing
		}
	}
	briefing, err := generateBriefing(ctx, item, s)
	if err != nil || briefing == "" {
		fmt.Fprintf(s.Out, "[Context] briefing unavailable (%v); falling back to overlap\n", err)
		return ""
	}
	payload, _ := json.MarshalIndent(map[string]string{"briefing": briefing}, "", "  ")
	_ = os.WriteFile(path, payload, 0o644)
	return "- SUBTITLE BRIEFING:\n" + briefing
}

func generateBriefing(ctx context.Context, item QueueItem, s *config.TranslationSession) (string, error) {
	var b strings.Builder
	for _, d := range item.Dialogues {
		fmt.Fprintf(&b, "[%d] %s\n", d.ID, SanitizeLineBreaks(d.Text))
	}
	prompt := "Analyze the following subtitle file and produce a concise briefing for a translator: " +
		"genre, tone, setting, synopsis and recurring proper names/terms (canonical spelling). " +
		"Keep it under 200 words. Do NOT translate anything.\n\n" + b.String()

	provider, err := ensureProvider(s)
	if err != nil {
		return "", err
	}
	res, err := provider.GenerateCompletion(ctx, prompt, 800, s)
	if err != nil || res == nil {
		return "", fmt.Errorf("briefing request failed")
	}
	if res.CachedTokens > 0 {
		s.TotalCachedTokens += res.CachedTokens
	}
	return strings.TrimSpace(res.Content), nil
}
