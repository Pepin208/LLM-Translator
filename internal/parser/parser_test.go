package parser

import (
	"reflect"
	"testing"

	"github.com/Pepin208/LLM-Translator/internal/subtitle"
)

func TestExtractSubtitlesMasksTags(t *testing.T) {
	f := &subtitle.File{Lines: []*subtitle.Line{
		{Index: 0, Text: `{\i1}Hello{\i0} world`, Style: "Default"},
		{Index: 1, Text: "OP song", Style: "OP"},
		{Index: 2, IsComment: true, Text: "note"},
		{Index: 3, Text: "   ", Style: "Default"},
	}}
	dialogues, tagMaps := ExtractSubtitles(f, nil)

	if len(dialogues) != 1 {
		t.Fatalf("dialogues = %d, want 1", len(dialogues))
	}
	if dialogues[0].Text != "<t0/>Hello<t1/> world" {
		t.Errorf("masked text = %q", dialogues[0].Text)
	}
	if !reflect.DeepEqual(tagMaps[0], []string{`{\i1}`, `{\i0}`}) {
		t.Errorf("tag map = %v", tagMaps[0])
	}
}

func TestRestoreTagsLIFO(t *testing.T) {
	tagMap := []string{`{\i1}`, `{\i0}`}
	got := RestoreTags("<t0/>Hello<t1/>", tagMap)
	if got != `{\i1}Hello{\i0}` {
		t.Errorf("restore = %q", got)
	}
}

func TestRestoreTagsTolerant(t *testing.T) {
	if got := RestoreTags("<t 0 >Hola", []string{"X"}); got != "XHola" {
		t.Errorf("tolerant restore = %q", got)
	}
}

func TestRestoreTagsFallbackPrependsLost(t *testing.T) {
	got := RestoreTags("Hola", []string{`{\fad(1,1)}`})
	if got != `{\fad(1,1)}Hola` {
		t.Errorf("fallback restore = %q", got)
	}
}

func TestRestoreTagsCleanupOrphan(t *testing.T) {
	if got := RestoreTags("<t5/>Hola", nil); got != "Hola" {
		t.Errorf("orphan cleanup = %q", got)
	}
}

func TestRestoreTagsLineBreaks(t *testing.T) {
	got := RestoreTags("a<br/>b<nbr/>c", nil)
	if got != `a\Nb\nc` {
		t.Errorf("line breaks = %q", got)
	}
}

func TestExtractProperNames(t *testing.T) {
	f := &subtitle.File{Lines: []*subtitle.Line{
		{Index: 0, Text: "[KAMINA] Let's go!", Style: "Default"},
		{Index: 1, Text: "[KAMINA] again", Style: "Default"},
		{Index: 2, Text: "SIMON: I agree", Style: "Default"},
		{Index: 3, Text: "SIMON: yes", Style: "Default"},
		{Index: 4, Text: "the end", Style: "Default"},
	}}
	names := ExtractProperNames(f)
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["Kamina"] || !found["Simon"] {
		t.Errorf("names = %v", names)
	}
}
