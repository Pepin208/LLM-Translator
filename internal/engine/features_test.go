package engine

import (
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	"translate_llm/internal/config"
	"translate_llm/internal/parser"
	"translate_llm/internal/providers"
)

// splitProvider returns a truncated response on the first call, then complete
// responses, to exercise the adaptive batch split.
type splitProvider struct{ calls int }

var splitIDRe = regexp.MustCompile(`^\[(\d+)\]\s*:?\s*(?:[^:]+:\s*)?(.*)$`)

func (p *splitProvider) GenerateCompletion(_ context.Context, payload string, _ int, _ *config.TranslationSession) (*providers.CompletionResult, error) {
	p.calls++
	var out []string
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "[C]:") || strings.HasPrefix(line, "[HINT]:") {
			continue
		}
		if m := splitIDRe.FindStringSubmatch(line); m != nil {
			out = append(out, m[1]+"|T"+m[2])
		}
	}
	res := &providers.CompletionResult{Content: strings.Join(out, "\n"), PromptTokens: 5, CompletionTokens: 5}
	if p.calls == 1 {
		// Simulate a truncated response that only contains the first line.
		if len(out) > 1 {
			out = out[:1]
		}
		res.Content = strings.Join(out, "\n")
		res.FinishReason = "length"
	}
	return res, nil
}

func TestAdaptiveSplitOnTruncation(t *testing.T) {
	s := config.NewSession()
	s.Out = io.Discard
	s.ProviderInstance = &splitProvider{}
	s.ActiveProvider = "OpenRouter"
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	s.ModelID = "mock"
	s.DynamicSystemPrompt = "sp"

	pairs := []Pair{{1, "one thing", ""}, {2, "two things", ""}, {3, "three things", ""}, {4, "four things", ""}}
	out := ProcessTranslationBatch(context.Background(), pairs, s, 600, nil)
	if out.Recovered != 4 {
		t.Fatalf("recovered = %d, want 4 (status=%s)", out.Recovered, out.Status)
	}
	if out.Status != "OK_SPLIT" {
		t.Errorf("status = %q, want OK_SPLIT", out.Status)
	}
}

func TestEffectiveBatchSize(t *testing.T) {
	s := config.NewSession()
	if got := effectiveBatchSize(s); got != config.ChunkSize {
		t.Errorf("default = %d", got)
	}
	s.BatchSize = 10
	if got := effectiveBatchSize(s); got != 10 {
		t.Errorf("override = %d", got)
	}
}

func TestFullContextBlock(t *testing.T) {
	s := config.NewSession()
	s.ContextLength = 128000
	item := QueueItem{Dialogues: []parser.Dialogue{
		{ID: 0, Text: "Hello", Name: ""},
		{ID: 1, Text: "World", Name: ""},
	}}
	block := fullContextBlock(item, s)
	if !strings.Contains(block, "[0] Hello") || !strings.Contains(block, "[1] World") {
		t.Errorf("full context block = %q", block)
	}
}

// bareProvider returns a translation without the "ID|" prefix, to exercise the
// single-pair fallback parser.
type bareProvider struct{}

func (bareProvider) GenerateCompletion(_ context.Context, _ string, _ int, _ *config.TranslationSession) (*providers.CompletionResult, error) {
	return &providers.CompletionResult{Content: "Voy a convertirme en Cazador"}, nil
}

func TestSinglePairFallbackWithoutID(t *testing.T) {
	s := config.NewSession()
	s.Out = io.Discard
	s.ProviderInstance = bareProvider{}
	s.ActiveProvider = "OpenRouter"
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	s.ModelID = "mock"
	s.DynamicSystemPrompt = "sp"

	out := ProcessTranslationBatch(context.Background(), []Pair{{84, "I'm gonna become a Hunter!", ""}}, s, 600, nil)
	if out.Recovered != 1 {
		t.Fatalf("single-pair fallback failed: recovered=%d status=%s", out.Recovered, out.Status)
	}
	if got := s.Cache[s.CacheKey(84, "I'm gonna become a Hunter!")]; got != "Voy a convertirme en Cazador" {
		t.Errorf("cached = %q", got)
	}

	// The fallback must NOT apply when there is more than one pair.
	s2 := config.NewSession()
	s2.Out = io.Discard
	s2.ProviderInstance = bareProvider{}
	s2.SourceLang = "EN"
	s2.TargetLang = "ES"
	s2.ModelID = "mock"
	s2.DynamicSystemPrompt = "sp"
	out2 := ProcessTranslationBatch(context.Background(), []Pair{{1, "one thing", ""}, {2, "two things", ""}}, s2, 600, nil)
	if out2.Recovered != 0 {
		t.Errorf("fallback must not apply to multi-pair batches: recovered=%d", out2.Recovered)
	}
}

func TestBriefingContextBlockCaches(t *testing.T) {
	dir := t.TempDir()
	s := config.NewSession()
	s.Out = io.Discard
	s.ProviderInstance = &splitProvider{}
	s.ActiveProvider = "OpenRouter"
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	s.ModelID = "mock"
	s.DynamicSystemPrompt = "sp"

	item := QueueItem{
		BaseDir:  dir,
		BaseName: "movie",
		Dialogues: []parser.Dialogue{
			{ID: 0, Text: "Hello", Name: ""},
			{ID: 1, Text: "World", Name: ""},
		},
	}
	block := briefingContextBlock(context.Background(), item, s)
	if !strings.Contains(block, "SUBTITLE BRIEFING") {
		t.Errorf("briefing block = %q", block)
	}
	// Second call must reuse the cached file and not hit the provider again.
	before := s.ProviderInstance.(*splitProvider).calls
	block2 := briefingContextBlock(context.Background(), item, s)
	after := s.ProviderInstance.(*splitProvider).calls
	if block2 != block {
		t.Errorf("cached briefing differs")
	}
	if after != before {
		t.Errorf("briefing not cached: calls %d -> %d", before, after)
	}
}
