package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/providers"
	"github.com/Pepin208/LLM-Translator/internal/utils"
)

// mockProvider echoes the masked payload prefixed with "T", preserving
// placeholders so tag restoration can be verified end-to-end.
type mockProvider struct{}

func (mockProvider) GenerateCompletion(_ context.Context, payload string, _ int, _ *config.TranslationSession) (*providers.CompletionResult, error) {
	idRe := regexp.MustCompile(`^\[(\d+)\]\s*:?\s*(?:[^:]+:\s*)?(.*)$`)
	var out []string
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "[C]:") || strings.HasPrefix(line, "[HINT]:") {
			continue
		}
		m := idRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, m[1]+"|T"+m[2])
	}
	return &providers.CompletionResult{Content: strings.Join(out, "\n"), PromptTokens: 10, CompletionTokens: 10}, nil
}

func TestEndToEndWithMockProvider(t *testing.T) {
	dir := t.TempDir()
	in := "1\n00:00:01,000 --> 00:00:03,000\nHello there\n\n" +
		"2\n00:00:04,000 --> 00:00:06,000\n<i>Goodbye</i> friend\n"
	inPath := filepath.Join(dir, "episode_01.srt")
	if err := os.WriteFile(inPath, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	s := config.NewSession()
	s.Out = io.Discard
	s.ProviderInstance = mockProvider{}
	s.ActiveProvider = "OpenRouter"
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	s.ModelID = "mock-model"
	s.DynamicSystemPrompt = config.BuildSystemPrompt("EN", "ES", "mock-model", "")
	s.ContextLength = 128000

	queue, _, _ := BuildExecutionQueue([]string{inPath}, nil, s, nil, "USD")
	if len(queue) != 1 {
		t.Fatalf("queue = %d, want 1", len(queue))
	}

	ExecuteTranslationQueue(context.Background(), queue, s, nil, "USD", 1, dir)

	outPath := filepath.Join(dir, "episode_01_es.srt")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "THello there") {
		t.Errorf("translation missing:\n%s", got)
	}
	if !strings.Contains(got, "T<i>Goodbye</i> friend") {
		t.Errorf("tags not restored correctly:\n%s", got)
	}
	if !strings.Contains(got, "00:00:04,000 --> 00:00:06,000") {
		t.Errorf("timings lost:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".episode_01_cache.json")); !os.IsNotExist(err) {
		t.Errorf("cache file should be removed after success")
	}
}

func TestParseExcludedRangesUsedByExtract(t *testing.T) {
	if got := utils.ParseExcludedRangesStr("00:02"); len(got) != 1 {
		t.Errorf("ranges = %v", got)
	}
}

// TestEndToEndSSA runs the full pipeline (mock provider, no network) over an
// SSA v4 file and asserts the output keeps the .ssa extension, the SSA event
// layout (Marked= field) and inline override tags.
func TestEndToEndSSA(t *testing.T) {
	dir := t.TempDir()
	in := "[Script Info]\n" +
		"ScriptType: v4.00\n\n" +
		"[V4 Styles]\n" +
		"Format: Name, Fontname, Fontsize, PrimaryColour\n" +
		"Style: Default,Arial,20,16777215\n\n" +
		"[Events]\n" +
		"Format: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: Marked=0,0:00:01.00,0:00:03.00,Default,Alice,0000,0000,0000,,{\\i1}Hello{\\i0} world\n"
	inPath := filepath.Join(dir, "episode_01.ssa")
	if err := os.WriteFile(inPath, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	s := config.NewSession()
	s.Out = io.Discard
	s.ProviderInstance = mockProvider{}
	s.ActiveProvider = "OpenRouter"
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	s.ModelID = "mock-model"
	s.DynamicSystemPrompt = config.BuildSystemPrompt("EN", "ES", "mock-model", "")
	s.ContextLength = 128000

	queue, _, _ := BuildExecutionQueue([]string{inPath}, nil, s, nil, "USD")
	if len(queue) != 1 {
		t.Fatalf("queue = %d, want 1", len(queue))
	}
	ExecuteTranslationQueue(context.Background(), queue, s, nil, "USD", 1, dir)

	outPath := filepath.Join(dir, "episode_01_es.ssa")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "Marked=0,0:00:01.00,0:00:03.00,Default,Alice,") {
		t.Errorf("SSA event layout/timings lost:\n%s", got)
	}
	if !strings.Contains(got, `{\i1}`) || !strings.Contains(got, `{\i0}`) {
		t.Errorf("inline override tags not preserved:\n%s", got)
	}
	if !strings.Contains(got, "Hello") {
		t.Errorf("translated text missing:\n%s", got)
	}
}
