package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"translate_llm/internal/config"
)

const realUpload = "../../web_storage/uploads/44d3cf99_Hunter.x.Hunter.S01E02.Test.x.Of.x.Tests.WEBRip.AMZN.en.srt"

// TestRealFileLineBreakIntegrity runs the full pipeline (mock provider, no
// network) over a real uploaded subtitle and asserts that multi-line cues are
// emitted with real newlines, never SSA-style \N / \n literals.
func TestRealFileLineBreakIntegrity(t *testing.T) {
	data, err := os.ReadFile(realUpload)
	if err != nil {
		t.Skip("real upload not present")
	}
	dir := t.TempDir()
	inPath := filepath.Join(dir, filepath.Base(realUpload))
	if err := os.WriteFile(inPath, data, 0o644); err != nil {
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
	ExecuteTranslationQueue(context.Background(), queue, s, nil, "USD", 1, dir)

	outPath := filepath.Join(dir, "44d3cf99_Hunter.x.Hunter.S01E02.Test.x.Of.x.Tests.WEBRip.AMZN.en_es.srt")
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	got := string(out)

	if strings.Contains(got, `\N`) || strings.Contains(got, `\n`) {
		t.Errorf("SSA escapes leaked into SRT output")
	}

	// Count multi-line cues in source and output.
	cueRe := regexp.MustCompile(`(?m)^\d+\r?\n\d{2}:\d{2}:\d{2},\d{3} --> .*\r?\n((?:.*\r?\n?)*?)(?:\r?\n|$)`)
	srcMulti := countMultiLineCues(string(data), cueRe)
	outMulti := countMultiLineCues(got, cueRe)
	if srcMulti == 0 {
		t.Fatalf("source has no multi-line cues; test is meaningless")
	}
	if outMulti != srcMulti {
		t.Errorf("multi-line cues: source=%d output=%d", srcMulti, outMulti)
	}
}

func countMultiLineCues(text string, cueRe *regexp.Regexp) int {
	count := 0
	for _, m := range cueRe.FindAllStringSubmatch(text, -1) {
		body := strings.TrimRight(m[1], "\r\n")
		if strings.Contains(body, "\n") {
			count++
		}
	}
	return count
}

// TestRealFileContextModes runs the pipeline with the new global-context modes
// (briefing / full) over the real uploaded subtitle, no network.
func TestRealFileContextModes(t *testing.T) {
	data, err := os.ReadFile(realUpload)
	if err != nil {
		t.Skip("real upload not present")
	}
	for _, mode := range []string{"briefing", "full"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			inPath := filepath.Join(dir, filepath.Base(realUpload))
			if err := os.WriteFile(inPath, data, 0o644); err != nil {
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
			s.ContextMode = mode

			queue, _, _ := BuildExecutionQueue([]string{inPath}, nil, s, nil, "USD")
			ExecuteTranslationQueue(context.Background(), queue, s, nil, "USD", 1, dir)

			outPath := filepath.Join(dir, "44d3cf99_Hunter.x.Hunter.S01E02.Test.x.Of.x.Tests.WEBRip.AMZN.en_es.srt")
			out, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("output not written: %v", err)
			}
			if strings.Contains(string(out), `\N`) || strings.Contains(string(out), `\n`) {
				t.Errorf("SSA escapes leaked into SRT output (%s mode)", mode)
			}
			if s.ContextBlock == "" {
				t.Errorf("%s mode did not inject a context block", mode)
			}
		})
	}
}
