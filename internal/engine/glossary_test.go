package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/providers"
	"github.com/Pepin208/LLM-Translator/internal/subtitle"
)

func TestSeriesKeyCollapsesEpisodes(t *testing.T) {
	a := SeriesKey("58b38b8c_Hunter.x.Hunter.S01E04.Hope.x.And.x.Ambition.WEBRip.AMZN.en.srt")
	b := SeriesKey("541d38a8_Hunter.x.Hunter.S01E03.Rivals.x.In.x.Survival.WEBRip.AMZN.en.srt")
	if a != b {
		t.Fatalf("series keys differ: %q vs %q", a, b)
	}
	if a != "Hunter x Hunter" {
		t.Errorf("series key = %q, want Hunter x Hunter", a)
	}
	if EpisodeKey("58b38b8c_Hunter.x.Hunter.S01E04.Hope.x.And.x.Ambition.WEBRip.AMZN.en.srt") != "S01E04" {
		t.Errorf("episode key = %q", EpisodeKey("58b38b8c_Hunter.x.Hunter.S01E04.Hope.x.And.x.Ambition.WEBRip.AMZN.en.srt"))
	}
	c := SeriesKey("075ea42b_Oppenheimer.2023.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-ESiR.SDH.En.ass")
	if !strings.Contains(c, "Oppenheimer") || strings.Contains(strings.ToLower(c), "1080p") {
		t.Errorf("movie key = %q", c)
	}
}

// jsonProvider returns enrichment JSON, adding a new location/term on the
// second call to exercise knowledge accumulation across episodes.
type jsonProvider struct{ calls int }

func (p *jsonProvider) GenerateCompletion(_ context.Context, _ string, _ int, _ *config.TranslationSession) (*providers.CompletionResult, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.CompletionResult{
			Content: `{"genre":["Anime","Action"],"characters":["Gon","Killua"],"character_genders":{"Gon":"m","Killua":"f"},"locations":["Whale Island"],"terms":["Hunter Association"],"summary":"Gon seeks his father."}`,
		}, nil
	}
	return &providers.CompletionResult{
		Content: `{"genre":["Anime"],"characters":["Hisoka"],"character_genders":{"Hisoka":"m"},"locations":["Heavens Arena"],"terms":["Nen"],"summary":"New arc."}`,
	}, nil
}

func sampleFile() *subtitle.File {
	return &subtitle.File{Lines: []*subtitle.Line{
		{Index: 0, Text: "[GON] Hello!", Style: "Default"},
		{Index: 1, Text: "KILLUA: let's go", Style: "Default"},
		{Index: 2, Text: "[GON] again", Style: "Default"},
		{Index: 3, Text: "KILLUA: yes", Style: "Default"},
	}}
}

func TestGlossaryPersistedAndReused(t *testing.T) {
	dir := t.TempDir()
	provider := &jsonProvider{}
	s := config.NewSession()
	s.Out = nil
	s.GlossaryDir = dir
	s.ProviderInstance = provider
	s.ActiveProvider = "OpenRouter"
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	s.ModelID = "mock"
	s.DynamicSystemPrompt = "sp"
	s.EnrichGlossary = true

	g := BuildSeriesGlossary(context.Background(), "Hunter x Hunter", []*subtitle.File{sampleFile()}, []string{"S01E04"}, s)
	if g == nil || len(g.Names) == 0 {
		t.Fatalf("no names extracted: %+v", g)
	}
	if len(g.Genre) == 0 || len(g.Locations) == 0 {
		t.Errorf("enrichment missing: %+v", g)
	}
	if g.Genders["Gon"] != "m" || g.Genders["Killua"] != "f" {
		t.Errorf("character genders missing: %+v", g.Genders)
	}
	if len(g.Episodes) != 1 || g.Episodes[0] != "S01E04" {
		t.Errorf("episode not recorded: %+v", g.Episodes)
	}
	if _, err := os.Stat(filepath.Join(dir, "Hunter x Hunter.json")); err != nil {
		t.Fatalf("glossary not saved: %v", err)
	}

	// A new episode must trigger a fresh enrichment that ACCUMULATES.
	g2 := BuildSeriesGlossary(context.Background(), "Hunter x Hunter", []*subtitle.File{sampleFile()}, []string{"S01E05"}, s)
	if !containsFold(g2.Locations, "Heavens Arena") || !containsFold(g2.Locations, "Whale Island") {
		t.Errorf("knowledge did not accumulate: %+v", g2.Locations)
	}
	if provider.calls != 2 {
		t.Errorf("expected a second enrichment call, calls=%d", provider.calls)
	}

	// Re-running the same episode must NOT call the provider again.
	_ = BuildSeriesGlossary(context.Background(), "Hunter x Hunter", []*subtitle.File{sampleFile()}, []string{"S01E05"}, s)
	if provider.calls != 2 {
		t.Errorf("same episode re-enriched: calls=%d", provider.calls)
	}

	loaded := LoadSeriesGlossary(dir, "Hunter x Hunter")
	if loaded == nil || len(loaded.Names) == 0 || len(loaded.Genre) == 0 {
		t.Fatalf("glossary not reused: %+v", loaded)
	}
	block := formatGlossaryForPrompt(loaded)
	for _, want := range []string{"Genre:", "Characters/Names:", "Places:", "Character genders"} {
		if !strings.Contains(block, want) {
			t.Errorf("prompt block missing %q: %q", want, block)
		}
	}
}
