package engine

import (
	"testing"

	"translate_llm/internal/config"
)

func TestSanitizeLineBreaks(t *testing.T) {
	cases := map[string]string{
		"a\nb":   "a<nbr/>b",
		`a\Nb`:   "a<br/>b",
		"a\r\nb": "a<nbr/>b",
	}
	for in, want := range cases {
		if got := SanitizeLineBreaks(in); got != want {
			t.Errorf("SanitizeLineBreaks(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeOutputText(t *testing.T) {
	if got := SanitizeOutputText("hola???", "", "EN"); got != "hola?" {
		t.Errorf("multi ? = %q", got)
	}
	if got := SanitizeOutputText("que paso?", "", "ES"); got != "¿que paso?" {
		t.Errorf("opening mark = %q", got)
	}
	if got := SanitizeOutputText("uno dos tres cuatro", "a<br/>b", "ES"); got != "uno dos<br/>tres cuatro" {
		t.Errorf("br restore = %q", got)
	}
}

func TestNormalizeGlossaryNames(t *testing.T) {
	glossary := []string{"Simón", "Kamina"}
	if got := NormalizeGlossaryNames("¡Simon! ¡KAMINA!", glossary); got != "¡Simón! ¡Kamina!" {
		t.Errorf("glossary = %q", got)
	}
	if got := NormalizeGlossaryNames("Simonization", glossary); got != "Simonization" {
		t.Errorf("boundary = %q", got)
	}
}

func TestValidateTokens(t *testing.T) {
	if valid, final := ValidateTokens(999999, 600, 128000); valid || final != 1 {
		t.Errorf("oversized = %v/%d", valid, final)
	}
	if valid, final := ValidateTokens(100, 600, 128000); !valid || final != 600 {
		t.Errorf("normal = %v/%d", valid, final)
	}
}

func TestCleanLLMResponse(t *testing.T) {
	if got := CleanLLMResponse("<think>hmm</think>1|Hola", "deepseek-chat"); got != "1|Hola" {
		t.Errorf("think strip = %q", got)
	}
	if got := CleanLLMResponse("```json\n1|Hola\n```", "gpt-4o"); got != "1|Hola" {
		t.Errorf("fence strip = %q", got)
	}
	if got := CleanLLMResponse("[t0]Hola", "gpt-4o"); got != "<t0/>Hola" {
		t.Errorf("bracket tag = %q", got)
	}
}

func TestQualityCheckEcho(t *testing.T) {
	s := config.NewSession()
	s.SourceLang = "EN"
	s.TargetLang = "ES"
	if reason := qualityCheckEcho("Hello world", "Hello world", s, nil); reason == "" {
		t.Errorf("expected echo rejection")
	}
	if reason := qualityCheckEcho("Hola mundo", "Hello world", s, nil); reason != "" {
		t.Errorf("unexpected rejection: %q", reason)
	}
}

func TestQualityCheckAnchors(t *testing.T) {
	bad, _ := qualityCheckAnchors("no numbers here", "Episode 3 arrives", nil)
	if !bad {
		t.Errorf("expected anchor rejection")
	}
	ok, _ := qualityCheckAnchors("El episodio 3 llega", "Episode 3 arrives", nil)
	if ok {
		t.Errorf("unexpected anchor rejection")
	}
}

func TestGetOverlapContext(t *testing.T) {
	s := config.NewSession()
	s.TargetLang = "ES"
	dialogues := []Pair{
		{ID: 0, Text: "one"},
		{ID: 1, Text: "two"},
		{ID: 2, Text: "three"},
	}
	s.Cache[s.CacheKey(0, "one")] = "uno"
	s.Cache[s.CacheKey(1, "two")] = "dos"
	ctx := GetOverlapContext(2, dialogues, s, -1)
	if len(ctx) != 2 || ctx[0] != "uno" || ctx[1] != "dos" {
		t.Errorf("overlap = %v", ctx)
	}
}
