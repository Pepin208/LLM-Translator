// Package subtitle implements a minimal SRT/ASS/SSA/VTT reader and writer that
// preserves raw dialogue text, style names and speaker names. This deliberately
// replaces go-astisub, which normalizes away the inline tags ({\fad...}, <i>)
// that the tag-masking pipeline depends on.
package subtitle

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Line is a single subtitle event.
type Line struct {
	Index     int // sequential position among all events (comments included)
	IsComment bool
	Style     string
	Name      string
	StartMS   int
	EndMS     int
	Text      string // raw text, formatting tags preserved

	editIdx int // index into File.edits, or -1 when the event has no text
}

type edit struct {
	start int
	end   int
	repl  []string
}

// File is a parsed subtitle document plus enough state to rebuild it.
type File struct {
	Path   string
	Format string // "srt", "ass", "vtt"
	Lines  []*Line

	raw   []string
	edits []edit
}

// Parse loads a subtitle file, auto-detecting the format by extension.
func Parse(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	raw := strings.Split(text, "\n")
	// Preserve a trailing newline in the raw slice without an extra empty block.
	if len(raw) > 1 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}

	f := &File{Path: path, raw: raw}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ass", ".ssa":
		f.Format = "ass"
		if err := f.parseASS(raw); err != nil {
			return nil, err
		}
	case ".vtt":
		f.Format = "vtt"
		f.parseCues(raw, true)
	default:
		f.Format = "srt"
		f.parseCues(raw, false)
	}
	return f, nil
}

// --- ASS / SSA -------------------------------------------------------------

func (f *File) parseASS(raw []string) error {
	inEvents := false
	var fields []string
	dialogueCount := 0

	for i, line := range raw {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inEvents = strings.EqualFold(trimmed, "[Events]")
			continue
		}
		if !inEvents {
			continue
		}
		if strings.HasPrefix(trimmed, "Format:") {
			fields = splitASSFields(trimmed[len("Format:"):])
			continue
		}

		colon := strings.Index(trimmed, ":")
		if colon < 0 {
			continue
		}
		kind := strings.TrimSpace(trimmed[:colon])
		isComment := strings.EqualFold(kind, "Comment")
		if !isComment && !strings.EqualFold(kind, "Dialogue") {
			continue
		}

		body := strings.TrimSpace(trimmed[colon+1:])
		parts := splitASS(body, len(fields))
		ev := &Line{Index: dialogueCount, IsComment: isComment, editIdx: len(f.edits)}
		dialogueCount++

		if len(parts) > 0 {
			ev.StartMS = parseASSTime(getField(parts, fields, "Start"))
			ev.EndMS = parseASSTime(getField(parts, fields, "End"))
			ev.Style = strings.TrimSpace(getField(parts, fields, "Style"))
			ev.Name = strings.TrimSpace(getField(parts, fields, "Name"))
			ev.Text = getField(parts, fields, "Text")
		}

		f.Lines = append(f.Lines, ev)

		// Reconstruct: keep everything up to the Text field, replace it.
		prefix := line[:colon+1] + " "
		for idx := 0; idx < len(fields)-1 && idx < len(parts); idx++ {
			if fields[idx] == "Text" {
				break
			}
			prefix += parts[idx] + ","
		}
		if !strings.HasSuffix(prefix, ",") {
			prefix += " "
		}
		f.edits = append(f.edits, edit{start: i, end: i + 1, repl: []string{prefix}})
	}
	return nil
}

func splitASSFields(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// splitASS splits an event body into at most n fields, keeping commas and
// trailing spaces inside the final (Text) field.
func splitASS(body string, n int) []string {
	if n <= 0 {
		return []string{body}
	}
	parts := strings.SplitN(body, ",", n)
	return parts
}

func getField(parts, fields []string, name string) string {
	for i, f := range fields {
		if strings.EqualFold(f, name) && i < len(parts) {
			return parts[i]
		}
	}
	return ""
}

func parseASSTime(v string) int {
	v = strings.TrimSpace(v)
	dot := strings.Index(v, ".")
	frac := ""
	mainPart := v
	if dot >= 0 {
		mainPart = v[:dot]
		frac = v[dot+1:]
	}
	parts := strings.Split(mainPart, ":")
	ms := 0
	switch len(parts) {
	case 3:
		h, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		m, _ := strconv.Atoi(parts[1])
		s, _ := strconv.Atoi(parts[2])
		ms = h*3600000 + m*60000 + s*1000
	case 2:
		m, _ := strconv.Atoi(parts[0])
		s, _ := strconv.Atoi(parts[1])
		ms = m*60000 + s*1000
	}
	if frac != "" {
		if len(frac) > 2 {
			frac = frac[:2]
		}
		for len(frac) < 2 {
			frac += "0"
		}
		c, _ := strconv.Atoi(frac)
		ms += c * 10
	}
	return ms
}

// --- SRT / VTT -------------------------------------------------------------

func (f *File) parseCues(raw []string, vtt bool) {
	blockStart := -1
	index := 0
	seenFirstCue := false

	flush := func(end int) {
		if blockStart < 0 {
			return
		}
		block := raw[blockStart:end]
		// Drop leading blank lines.
		for len(block) > 0 && strings.TrimSpace(block[0]) == "" {
			block = block[1:]
			blockStart++
		}
		if len(block) == 0 {
			blockStart = -1
			return
		}

		// NOTE block (WebVTT comment).
		if vtt && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(block[0])), "NOTE") {
			f.Lines = append(f.Lines, &Line{Index: index, IsComment: true, editIdx: -1})
			index++
			blockStart = -1
			return
		}

		timingRow := -1
		for i, l := range block {
			if strings.Contains(l, "-->") {
				timingRow = i
				break
			}
		}
		if timingRow < 0 {
			blockStart = -1
			return
		}

		startMS, endMS := parseCueTiming(block[timingRow])
		textLines := block[timingRow+1:]
		textStart := blockStart + timingRow + 1

		f.Lines = append(f.Lines, &Line{
			Index:   index,
			StartMS: startMS,
			EndMS:   endMS,
			Text:    strings.Join(textLines, "\n"),
			editIdx: len(f.edits),
		})
		index++
		f.edits = append(f.edits, edit{start: textStart, end: textStart + len(textLines)})
		blockStart = -1
	}

	for i := 0; i < len(raw); i++ {
		line := raw[i]
		if strings.TrimSpace(line) == "" {
			if blockStart >= 0 {
				flush(i)
			}
			continue
		}
		if !seenFirstCue {
			if vtt && strings.HasPrefix(strings.ToUpper(line), "WEBVTT") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "NOTE") {
				continue
			}
		}
		if blockStart < 0 {
			blockStart = i
		}
	}
	if blockStart >= 0 {
		flush(len(raw))
	}
}

func parseCueTiming(line string) (int, int) {
	parts := strings.SplitN(line, "-->", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])
	if idx := strings.Index(end, " "); idx >= 0 {
		end = end[:idx]
	}
	return parseSRTTime(start), parseSRTTime(end)
}

func parseSRTTime(v string) int {
	v = strings.TrimSpace(v)
	sep := ","
	if strings.Contains(v, ".") {
		sep = "."
	}
	dot := strings.Index(v, sep)
	mainPart := v
	frac := ""
	if dot >= 0 {
		mainPart = v[:dot]
		frac = v[dot+1:]
	}
	parts := strings.Split(mainPart, ":")
	ms := 0
	if len(parts) == 3 {
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		s, _ := strconv.Atoi(parts[2])
		ms = h*3600000 + m*60000 + s*1000
	} else if len(parts) == 2 {
		m, _ := strconv.Atoi(parts[0])
		s, _ := strconv.Atoi(parts[1])
		ms = m*60000 + s*1000
	}
	if frac != "" {
		if len(frac) > 3 {
			frac = frac[:3]
		}
		for len(frac) < 3 {
			frac += "0"
		}
		f, _ := strconv.Atoi(frac)
		ms += f
	}
	return ms
}

// --- Save ------------------------------------------------------------------

// Save writes the file back to disk applying translated texts (keyed by line
// Index). Tags are restored by the caller before invoking Save.
func (f *File) Save(path string, translations map[int]string) error {
	for _, ev := range f.Lines {
		if ev.editIdx < 0 || ev.editIdx >= len(f.edits) {
			continue
		}
		txt, ok := translations[ev.Index]
		if !ok {
			continue
		}
		if f.Format == "ass" {
			prefix := f.edits[ev.editIdx].repl[0]
			f.edits[ev.editIdx].repl = []string{prefix + txt}
		} else {
			// SRT/VTT are not SSA: a real newline is required, not the
			// SSA-style \N / \n escapes emitted by the engine.
			txt = strings.ReplaceAll(txt, `\N`, "\n")
			txt = strings.ReplaceAll(txt, `\n`, "\n")
			f.edits[ev.editIdx].repl = splitLinesPreserve(txt)
		}
	}

	replacements := map[int]edit{}
	for _, e := range f.edits {
		replacements[e.start] = e
	}

	var out []string
	i := 0
	for i < len(f.raw) {
		if e, ok := replacements[i]; ok {
			out = append(out, e.repl...)
			i = e.end
			continue
		}
		out = append(out, f.raw[i])
		i++
	}

	content := strings.Join(out, "\n")
	if strings.HasSuffix(content, "\n") {
		content = content[:len(content)-1]
	}
	content += "\n"

	return os.WriteFile(path, []byte(content), 0o644)
}

func splitLinesPreserve(text string) []string {
	if text == "" {
		return []string{""}
	}
	return strings.Split(text, "\n")
}

// String is a debugging helper.
func (f *File) String() string {
	return fmt.Sprintf("<subtitle.File format=%s lines=%d>", f.Format, len(f.Lines))
}
