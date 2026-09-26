package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/parser"
	"github.com/Pepin208/LLM-Translator/internal/subtitle"
)

// SeriesGlossary is the persistent, reusable knowledge base for a series.
type SeriesGlossary struct {
	Series    string            `json:"series"`
	Genre     []string          `json:"genre,omitempty"`
	Names     []string          `json:"names,omitempty"`
	Locations []string          `json:"locations,omitempty"`
	Terms     []string          `json:"terms,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Genders   map[string]string `json:"character_genders,omitempty"`
	// Episodes lists the episodes already analyzed so we do not re-enrich them.
	Episodes []string `json:"episodes,omitempty"`
}

// episodeTokenRe extracts a season/episode token from a filename.
var episodeTokenRe = regexp.MustCompile(`(?i)\b(S\d{1,2}E\d{1,3}|E\d{1,3}|EP?\d{1,3})\b`)

// EpisodeKey returns a stable per-episode identifier ("S01E04"), or the cleaned
// basename for movies/specials.
func EpisodeKey(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	base = uuidPrefixRe.ReplaceAllString(base, "")
	if m := episodeTokenRe.FindString(base); m != "" {
		return strings.ToUpper(m)
	}
	return strings.TrimSpace(base)
}

// uuidPrefix matches the 8-hex upload prefix.
var uuidPrefixRe = regexp.MustCompile(`^[0-9a-fA-F]{8}_`)

// seasonEpisodeRe cuts the filename at a season/episode marker.
var seasonEpisodeRe = regexp.MustCompile(`(?i)[\s._-]+(S\d{1,2}(E\d{1,3})?|E\d{1,3}|EP?\d{1,3})\b.*$`)

// releaseTags are stripped from the inferred series name.
var releaseTags = map[string]struct{}{
	"1080p": {}, "720p": {}, "2160p": {}, "4k": {}, "webrip": {}, "web": {},
	"web-dl": {}, "webdl": {}, "bluray": {}, "blu-ray": {}, "brrip": {},
	"bdrip": {}, "hdtv": {}, "dvdrip": {}, "x264": {}, "x265": {}, "h264": {},
	"h265": {}, "hevc": {}, "aac": {}, "ac3": {}, "dts": {}, "dts-hd": {},
	"amzn": {}, "nf": {}, "dsnp": {}, "atvp": {}, "sdr": {}, "hdr": {},
	"sdh": {}, "esir": {}, "proper": {}, "repack": {}, "multi": {}, "dual": {},
	"subs": {}, "eng": {},
}

// SeriesKey derives a stable, episode-independent key from a subtitle filename.
// "…Hunter.x.Hunter.S01E04.Hope…WEBRip.AMZN.en.srt" -> "Hunter x Hunter".
func SeriesKey(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	base = uuidPrefixRe.ReplaceAllString(base, "")

	if loc := seasonEpisodeRe.FindStringIndex(base); loc != nil {
		base = base[:loc[0]]
	}
	base = episodeSuffix.ReplaceAllString(base, "")
	base = regexp.MustCompile(`(?i)[\s._-](19|20)\d{2}\b`).ReplaceAllString(base, "")

	tokens := strings.FieldsFunc(base, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == ' '
	})
	kept := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, isTag := releaseTags[strings.ToLower(t)]; isTag {
			continue
		}
		kept = append(kept, t)
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

func glossaryPath(dir, series string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(series)
	safe = strings.TrimSpace(safe)
	if safe == "" {
		safe = "unknown"
	}
	return filepath.Join(dir, safe+".json")
}

// LoadSeriesGlossary reads a previously saved glossary (nil when absent).
func LoadSeriesGlossary(dir, series string) *SeriesGlossary {
	if dir == "" || series == "" {
		return nil
	}
	data, err := os.ReadFile(glossaryPath(dir, series))
	if err != nil {
		return nil
	}
	var g SeriesGlossary
	if json.Unmarshal(data, &g) != nil {
		return nil
	}
	return &g
}

// SaveSeriesGlossary persists the glossary.
func SaveSeriesGlossary(dir string, g *SeriesGlossary) error {
	if dir == "" || g == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(glossaryPath(dir, g.Series), data, 0o644)
}

// BuildSeriesGlossary loads an existing series glossary (reused across
// episodes) or creates one, merging offline name extraction with an LLM
// enrichment pass. Enrichment runs when core fields are missing, when a new
// episode is seen, or when new names appear, so knowledge accumulates.
func BuildSeriesGlossary(ctx context.Context, series string, parsedFiles []*subtitle.File, episodeKeys []string, s *config.TranslationSession) *SeriesGlossary {
	if series == "" {
		return nil
	}

	g := LoadSeriesGlossary(s.GlossaryDir, series)
	if g == nil {
		g = &SeriesGlossary{Series: series}
	}

	var offline []string
	for _, f := range parsedFiles {
		offline = mergeUnique(offline, parser.ExtractProperNames(f))
	}
	novelNames := 0
	for _, n := range offline {
		if !containsFold(g.Names, n) {
			novelNames++
		}
	}
	g.Names = mergeUnique(g.Names, offline)
	if len(g.Names) > 60 {
		g.Names = g.Names[:60]
	}

	newEpisode := false
	for _, e := range episodeKeys {
		if !contains(g.Episodes, e) {
			newEpisode = true
		}
	}

	if s.EnrichGlossary && (needsEnrichment(g) || newEpisode || novelNames > 0) {
		if enrichSeriesGlossary(ctx, g, collectContextText(parsedFiles), s) {
			g.Episodes = mergeUnique(g.Episodes, episodeKeys)
		}
	}

	_ = SaveSeriesGlossary(s.GlossaryDir, g)
	return g
}

// needsEnrichment reports whether core enrichment fields are still missing.
func needsEnrichment(g *SeriesGlossary) bool {
	return g.Summary == "" || len(g.Locations) == 0 || len(g.Genders) == 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// collectContextText builds a bounded "[id] text" dump for enrichment.
func collectContextText(files []*subtitle.File) string {
	var b strings.Builder
	count := 0
	const limit = 500
	for _, f := range files {
		for _, line := range f.Lines {
			if line.IsComment || count >= limit {
				continue
			}
			fmt.Fprintf(&b, "[%d] %s\n", line.Index, SanitizeLineBreaks(line.Text))
			count++
		}
	}
	return b.String()
}

// enrichSeriesGlossary runs one LLM pass to extract genre/locations/terms.
// Returns true when the glossary was actually updated.
func enrichSeriesGlossary(ctx context.Context, g *SeriesGlossary, text string, s *config.TranslationSession) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	provider, err := ensureProvider(s)
	if err != nil {
		return false
	}

	prompt := "Analyze this subtitle file and reply with ONLY compact JSON, no markdown:\n" +
		`{"genre":["..."],"characters":["..."],"character_genders":{"Name":"m|f|n"},"locations":["..."],"terms":["..."],"summary":"<one sentence>"}` + "\n" +
		"Extract the genre, recurring character names, the grammatical gender of each named character " +
		"(m=masculine, f=feminine, n=unknown/neutral), recurring place names, recurring terms, and a one-sentence summary. " +
		"Gender matters for correct grammatical agreement when translating. " +
		"Preserve proper-noun spelling. Do NOT translate.\n\n" + text

	res, err := provider.GenerateCompletion(ctx, prompt, 800, s)
	if err != nil || res == nil {
		if s.Out != nil {
			fmt.Fprintf(s.Out, "[Glossary] enrichment skipped: %v\n", err)
		}
		return false
	}
	if res.CachedTokens > 0 {
		s.TotalCachedTokens += res.CachedTokens
	}
	raw := strings.TrimSpace(res.Content)
	if i := strings.Index(raw, "{"); i >= 0 {
		raw = raw[i:]
	}
	if j := strings.LastIndex(raw, "}"); j >= 0 {
		raw = raw[:j+1]
	}
	var parsed struct {
		Genre      []string          `json:"genre"`
		Characters []string          `json:"characters"`
		Genders    map[string]string `json:"character_genders"`
		Locations  []string          `json:"locations"`
		Terms      []string          `json:"terms"`
		Summary    string            `json:"summary"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return false
	}
	g.Genre = mergeUnique(g.Genre, parsed.Genre)
	g.Names = mergeUnique(g.Names, parsed.Characters)
	g.Locations = mergeUnique(g.Locations, parsed.Locations)
	g.Terms = mergeUnique(g.Terms, parsed.Terms)
	if g.Genders == nil {
		g.Genders = map[string]string{}
	}
	for name, gender := range parsed.Genders {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		g.Genders[name] = normalizeGender(gender)
	}
	if parsed.Summary != "" {
		g.Summary = parsed.Summary
	}
	return true
}

// normalizeGender folds provider spellings into m/f/n.
func normalizeGender(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "m", "male", "masculine", "man", "boy", "he":
		return "m"
	case "f", "female", "feminine", "woman", "girl", "she":
		return "f"
	default:
		return "n"
	}
}

// formatGlossaryForPrompt renders a compact, token-cheap glossary block.
func formatGlossaryForPrompt(g *SeriesGlossary) string {
	if g == nil {
		return ""
	}
	var parts []string
	if len(g.Genre) > 0 {
		parts = append(parts, "Genre: "+strings.Join(g.Genre, ", "))
	}
	if len(g.Names) > 0 {
		parts = append(parts, "Characters/Names: "+strings.Join(g.Names, ", "))
	}
	if len(g.Locations) > 0 {
		parts = append(parts, "Places: "+strings.Join(g.Locations, ", "))
	}
	if len(g.Terms) > 0 {
		parts = append(parts, "Terms: "+strings.Join(g.Terms, ", "))
	}
	if len(g.Genders) > 0 {
		pairs := make([]string, 0, len(g.Genders))
		for name, gender := range g.Genders {
			label := gender
			switch gender {
			case "m":
				label = "masculine"
			case "f":
				label = "feminine"
			default:
				label = "unknown"
			}
			pairs = append(pairs, name+"="+label)
		}
		sort.Strings(pairs)
		parts = append(parts, "Character genders (apply correct grammatical agreement): "+strings.Join(pairs, ", "))
	}
	if g.Summary != "" {
		parts = append(parts, "Summary: "+g.Summary)
	}
	if len(parts) == 0 {
		return ""
	}
	return "- SERIES GLOSSARY (preserve exact spelling; do not translate proper nouns):\n  " +
		strings.Join(parts, "\n  ")
}

func mergeUnique(dst, src []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(dst)+len(src))
	for _, v := range append(append([]string{}, dst...), src...) {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
