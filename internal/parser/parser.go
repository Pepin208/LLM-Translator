// Package parser implements the symmetric tag masking/restoration pipeline and
// proper-name extraction, mirroring the Python core/parser.py + parts of utils.py.
package parser

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/subtitle"
	"github.com/Pepin208/LLM-Translator/internal/utils"
)

// Dialogue is a translatable, masked line.
type Dialogue struct {
	ID   int
	Text string
	Name string
}

// ExtractSubtitles masks formatting tags from a parsed subtitle file.
//
// It runs the ASS regex before the VTT regex to keep deterministic ordering
// and stores the original tags in order in the returned map.
func ExtractSubtitles(f *subtitle.File, excludedRanges [][2]int) ([]Dialogue, map[int][]string) {
	valid := make([]Dialogue, 0, len(f.Lines))
	tagMaps := map[int][]string{}

	for _, line := range f.Lines {
		if line.IsComment || strings.TrimSpace(line.Text) == "" || strings.HasPrefix(line.Text, "m ") {
			continue
		}
		if line.Style != "" && config.IsStyleExcluded(line.Style) {
			continue
		}
		if inExcluded(line.StartMS, excludedRanges) {
			continue
		}

		var tags []string
		replacer := func(m string) string {
			tags = append(tags, m)
			return "<t" + itoa(len(tags)-1) + "/>"
		}

		clean := config.ASSTagRegex.ReplaceAllStringFunc(line.Text, replacer)
		clean = config.VTTTagRegex.ReplaceAllStringFunc(clean, replacer)

		stripped := strings.TrimSpace(clean)
		if stripped == "" || onlyPlaceholders.MatchString(stripped) {
			continue
		}

		valid = append(valid, Dialogue{ID: line.Index, Text: clean, Name: line.Name})
		tagMaps[line.Index] = tags
	}

	return valid, tagMaps
}

func inExcluded(start int, ranges [][2]int) bool {
	for _, r := range ranges {
		if r[0] <= start && start <= r[1] {
			return true
		}
	}
	return false
}

var onlyPlaceholders = regexp.MustCompile(`^(\s*<\s*/?\s*t\s*\d+\s*/?\s*>\s*)+$`)

// --- Placeholder regexes ---------------------------------------------------

var (
	placeholderAny = regexp.MustCompile(`<\s*/?\s*t\s*\d+\s*/?\s*>`)
	placeholderMu  sync.Mutex
	placeholderRe  = map[int]*regexp.Regexp{}
)

func placeholderFor(index int) *regexp.Regexp {
	placeholderMu.Lock()
	defer placeholderMu.Unlock()
	if re, ok := placeholderRe[index]; ok {
		return re
	}
	re := regexp.MustCompile(`(?i)<\s*/?\s*t\s*` + itoa(index) + `\s*/?\s*>`)
	placeholderRe[index] = re
	return re
}

// RestoreTags restores native formatting tags from masked placeholders using a
// tolerant LIFO pass, re-anchoring any tags the model dropped.
func RestoreTags(translated string, tagMap []string) string {
	result := translated
	foundTags := len(placeholderAny.FindAllString(result, -1))
	expected := len(tagMap)

	restored := map[int]struct{}{}
	for i := len(tagMap) - 1; i >= 0; i-- {
		re := placeholderFor(i)
		if re.MatchString(result) {
			result = replaceFirst(re, result, tagMap[i])
			restored[i] = struct{}{}
		}
	}

	var lost []string
	for i := 0; i < len(tagMap); i++ {
		if _, ok := restored[i]; !ok {
			lost = append(lost, tagMap[i])
		}
	}
	if len(lost) > 0 {
		result = strings.Join(lost, "") + result
	}

	result = placeholderAny.ReplaceAllString(result, "")
	result = regexp.MustCompile(`(?i)<\s*br\s*/?>`).ReplaceAllString(result, `\N`)
	result = regexp.MustCompile(`(?i)<\s*nbr\s*/?>`).ReplaceAllString(result, `\n`)
	result = multiSpace.ReplaceAllString(result, " ")

	_ = foundTags
	_ = expected
	return strings.TrimSpace(result)
}

var multiSpace = regexp.MustCompile(` {2,}`)

func replaceFirst(re *regexp.Regexp, s, repl string) string {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return s
	}
	return s[:loc[0]] + repl + s[loc[1]:]
}

// Reconstruct restores tags and writes the translated file.
func Reconstruct(f *subtitle.File, translations map[int]string, tagMaps map[int][]string, outPath string) error {
	final := map[int]string{}
	for id, text := range translations {
		if tags, ok := tagMaps[id]; ok {
			final[id] = RestoreTags(text, tags)
		}
	}
	// Preserve untranslated lines verbatim.
	for _, line := range f.Lines {
		if _, ok := final[line.Index]; ok {
			continue
		}
		if _, ok := tagMaps[line.Index]; ok {
			final[line.Index] = line.Text
		}
	}
	return f.Save(outPath, final)
}

// --- Proper names ----------------------------------------------------------

// ExtractProperNames extracts consistent speaker/character names from a file.
func ExtractProperNames(f *subtitle.File) []string {
	nameCounter := map[string]int{}
	markerCounter := map[string]int{}
	seenInName := map[string]struct{}{}
	nameOrder := []string{}
	markerOrder := []string{}

	for _, line := range f.Lines {
		if line.IsComment {
			continue
		}
		text := asTagRegex.ReplaceAllString(line.Text, "")

		rawName := strings.TrimSpace(line.Name)
		if rawName != "" {
			rawName = vocalPrefix.ReplaceAllString(rawName, "")
			rawName = strings.TrimSpace(rawName)
			key := titleCase(rawName)
			_, stop := utils.NameStopwords[strings.ToLower(key)]
			if key != "" && !stop && len([]rune(rawName)) > 1 {
				if _, seen := seenInName[key]; !seen {
					seenInName[key] = struct{}{}
					nameCounter[key]++
					nameOrder = append(nameOrder, key)
				}
			}
		}

		for _, m := range nameMarker.FindAllStringSubmatch(text, -1) {
			raw := m[1]
			if raw == "" {
				raw = m[2]
			}
			key := titleCase(strings.TrimSpace(raw))
			_, stop := utils.NameStopwords[strings.ToLower(key)]
			if stop || len([]rune(key)) <= 1 {
				continue
			}
			if _, ok := markerCounter[key]; !ok {
				markerOrder = append(markerOrder, key)
			}
			markerCounter[key]++
		}
	}

	var ranked []string
	for _, key := range nameOrder {
		for i := 0; i < nameCounter[key]; i++ {
			ranked = append(ranked, key)
		}
	}

	type mc struct {
		key   string
		count int
	}
	markers := make([]mc, 0, len(markerCounter))
	for _, k := range markerOrder {
		markers = append(markers, mc{k, markerCounter[k]})
	}
	sort.SliceStable(markers, func(i, j int) bool { return markers[i].count > markers[j].count })
	if len(markers) > 40 {
		markers = markers[:40]
	}
	for _, m := range markers {
		if _, ok := nameCounter[m.key]; ok {
			continue
		}
		if m.count >= 2 {
			ranked = append(ranked, m.key)
		}
	}

	seen := map[string]struct{}{}
	result := []string{}
	for _, key := range ranked {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
		if len(result) >= 20 {
			break
		}
	}
	return result
}

var (
	asTagRegex  = regexp.MustCompile(`\{.*?\}`)
	vocalPrefix = regexp.MustCompile(`(?i)^vocal:\s*`)
	nameMarker  = regexp.MustCompile(`\[([A-Z][A-Za-z0-9 .'\-]{1,30})\]|([A-Z][A-Za-z0-9 .'\-]{1,30}):`)
)

func titleCase(s string) string {
	var b strings.Builder
	prevLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			if prevLetter {
				b.WriteRune(unicode.ToLower(r))
			} else {
				b.WriteRune(unicode.ToUpper(r))
			}
			prevLetter = true
		} else {
			b.WriteRune(r)
			prevLetter = false
		}
	}
	return b.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
