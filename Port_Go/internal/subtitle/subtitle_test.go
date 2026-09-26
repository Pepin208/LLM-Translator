package subtitle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseAndSaveSRT(t *testing.T) {
	in := "1\n00:00:01,000 --> 00:00:03,000\nHello <i>world</i>\n\n2\n00:00:04,000 --> 00:00:06,000\nBye\n"
	path := writeTemp(t, "a.srt", in)

	f, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(f.Lines))
	}
	if f.Lines[0].Text != "Hello <i>world</i>" {
		t.Errorf("text = %q (tags must be preserved)", f.Lines[0].Text)
	}
	if f.Lines[0].StartMS != 1000 || f.Lines[1].StartMS != 4000 {
		t.Errorf("timings: %d %d", f.Lines[0].StartMS, f.Lines[1].StartMS)
	}

	out := filepath.Join(t.TempDir(), "out.srt")
	if err := f.Save(out, map[int]string{0: "Hola <i>mundo</i>", 1: "Adiós"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	s := string(data)
	if !strings.Contains(s, "Hola <i>mundo</i>") || !strings.Contains(s, "Adiós") {
		t.Errorf("output missing translations:\n%s", s)
	}
	if !strings.Contains(s, "00:00:01,000 --> 00:00:03,000") {
		t.Errorf("timing line lost:\n%s", s)
	}
}

func TestParseAndSaveASS(t *testing.T) {
	in := "[Script Info]\nTitle: X\n\n" +
		"[V4+ Styles]\nFormat: Name, Fontname\nStyle: Default,Arial\nStyle: OP,Arial\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:03.00,Default,Alice,0,0,0,,{\\i1}Hello{\\i0} world\n" +
		"Comment: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,a note\n"
	path := writeTemp(t, "a.ass", in)

	f, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(f.Lines))
	}
	if f.Lines[0].Style != "Default" || f.Lines[0].Name != "Alice" {
		t.Errorf("style/name = %q/%q", f.Lines[0].Style, f.Lines[0].Name)
	}
	if f.Lines[0].Text != `{\i1}Hello{\i0} world` {
		t.Errorf("text = %q", f.Lines[0].Text)
	}
	if !f.Lines[1].IsComment {
		t.Errorf("second event should be a comment")
	}

	out := filepath.Join(t.TempDir(), "out.ass")
	if err := f.Save(out, map[int]string{0: `{\i1}Hola{\i0} mundo`}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	s := string(data)
	if !strings.Contains(s, `Dialogue: 0,0:00:01.00,0:00:03.00,Default,Alice,0,0,0,,{\i1}Hola{\i0} mundo`) {
		t.Errorf("ASS dialogue line not reconstructed:\n%s", s)
	}
	if !strings.Contains(s, "Comment: 0,0:00:00.00") {
		t.Errorf("comment lost:\n%s", s)
	}
}

func TestSaveSRTConvertsSSALineBreaks(t *testing.T) {
	in := "1\n00:00:01,000 --> 00:00:03,000\nline one\nline two\n"
	path := writeTemp(t, "b.srt", in)
	f, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.srt")
	if err := f.Save(out, map[int]string{0: `linea uno\Nlinea dos`}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	s := string(data)
	if strings.Contains(s, `\N`) {
		t.Errorf("SSA \\N leaked into SRT:\n%s", s)
	}
	if !strings.Contains(s, "linea uno\nlinea dos") {
		t.Errorf("line break not restored as newline:\n%s", s)
	}
}

func TestParseVTT(t *testing.T) {
	in := "WEBVTT\n\n1\n00:00:01.000 --> 00:00:03.000\nHello\n\nNOTE a comment\n"
	path := writeTemp(t, "a.vtt", in)
	f, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Lines) < 1 || f.Lines[0].Text != "Hello" {
		t.Fatalf("vtt parse = %+v", f.Lines)
	}
	if f.Lines[0].StartMS != 1000 {
		t.Errorf("start = %d", f.Lines[0].StartMS)
	}
}
