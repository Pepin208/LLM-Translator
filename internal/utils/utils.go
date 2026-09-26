// Package utils holds text normalization and range parsing helpers.
package utils

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeNameKey returns a lowercase, accent-free key (NFKD + ASCII fold).
// "Simón" and "Simon" collapse to the same key.
func NormalizeNameKey(name string) string {
	if name == "" {
		return ""
	}
	decomposed := norm.NFKD.String(name)
	var b strings.Builder
	for _, r := range decomposed {
		if r < 128 {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// ParseFileRange parses "1-3,5" into sorted, deduplicated 0-based indices.
// An empty input selects every line.
func ParseFileRange(input string, total int) []int {
	if strings.TrimSpace(input) == "" {
		out := make([]int, total)
		for i := range out {
			out[i] = i
		}
		return out
	}

	seen := map[int]struct{}{}
	tokens := splitTrim(input, ",")
	for _, token := range tokens {
		if strings.Contains(token, "-") {
			parts := strings.Split(token, "-")
			if len(parts) == 2 && isAllDigits(parts[0]) && isAllDigits(parts[1]) {
				start, _ := strconv.Atoi(parts[0])
				end, _ := strconv.Atoi(parts[1])
				lo, hi := minMax(start, end)
				lo = max(1, lo)
				hi = min(total, hi)
				for i := lo; i <= hi; i++ {
					seen[i-1] = struct{}{}
				}
			}
		} else if isAllDigits(token) {
			val, _ := strconv.Atoi(token)
			val = max(1, min(val, total))
			seen[val-1] = struct{}{}
		}
	}

	out := make([]int, 0, len(seen))
	for i := range seen {
		out = append(out, i)
	}
	sortInts(out)
	return out
}

// ParseExcludedRangesStr parses "00:01-00:15, 00:02, 05:00-05:10" into
// millisecond (start,end) tuples, matching the MM:SS placeholder in the UI.
func ParseExcludedRangesStr(raw string) [][2]int {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var ranges [][2]int
	for _, token := range splitTrim(raw, ",") {
		if strings.Contains(token, "-") {
			parts := strings.Split(token, "-")
			if len(parts) == 2 && strings.Contains(parts[0], ":") && strings.Contains(parts[1], ":") {
				lo, hi := minMax(TimeToMs(parts[0]), TimeToMs(parts[1]))
				ranges = append(ranges, [2]int{lo, hi})
			}
		} else if strings.Contains(token, ":") {
			v := TimeToMs(token)
			ranges = append(ranges, [2]int{v, v})
		}
	}
	return ranges
}

// TimeToMs converts subtitle timestamps ("HH:MM:SS,mmm" / "HH:MM:SS.mmm") to ms.
func TimeToMs(timeStr string) int {
	timeStr = strings.TrimSpace(timeStr)
	ms := 0
	var mainPart string

	if idx := strings.Index(timeStr, "."); idx >= 0 {
		mainPart = timeStr[:idx]
		frac := timeStr[idx+1:]
		if len(frac) > 2 {
			frac = frac[:2]
		}
		for len(frac) < 2 {
			frac += "0"
		}
		v, _ := strconv.Atoi(frac)
		ms += v * 10
	} else if idx := strings.Index(timeStr, ","); idx >= 0 {
		mainPart = timeStr[:idx]
		frac := timeStr[idx+1:]
		if len(frac) > 3 {
			frac = frac[:3]
		}
		for len(frac) < 3 {
			frac += "0"
		}
		v, _ := strconv.Atoi(frac)
		ms += v
	} else {
		mainPart = timeStr
	}

	parts := strings.Split(mainPart, ":")
	switch len(parts) {
	case 3:
		h, _ := strconv.Atoi(parts[0])
		m, _ := strconv.Atoi(parts[1])
		s, _ := strconv.Atoi(parts[2])
		ms += h*3600000 + m*60000 + s*1000
	case 2:
		m, _ := strconv.Atoi(parts[0])
		s, _ := strconv.Atoi(parts[1])
		ms += m*60000 + s*1000
	}
	return ms
}

// GroupContiguousIndices groups strictly sequential indices.
func GroupContiguousIndices(indices []int) [][]int {
	if len(indices) == 0 {
		return nil
	}
	var groups [][]int
	current := []int{indices[0]}
	for i := 1; i < len(indices); i++ {
		if indices[i] == indices[i-1]+1 {
			current = append(current, indices[i])
		} else {
			groups = append(groups, current)
			current = []int{indices[i]}
		}
	}
	return append(groups, current)
}

// NameStopwords are excluded from glossary extraction.
var NameStopwords = map[string]struct{}{
	"the": {}, "and": {}, "you": {}, "for": {}, "that": {}, "with": {},
	"this": {}, "what": {}, "where": {}, "i": {}, "a": {}, "an": {}, "is": {},
	"it": {}, "are": {}, "was": {}, "were": {}, "to": {}, "of": {}, "in": {},
	"on": {}, "at": {}, "by": {}, "from": {}, "but": {}, "or": {}, "if": {},
	"then": {}, "so": {}, "no": {}, "yes": {}, "all": {}, "both": {}, "ok": {},
	"okay": {}, "hey": {}, "oh": {}, "ah": {}, "uh": {}, "um": {}, "dialogue": {},
	"default": {}, "sign": {}, "op": {}, "ed": {}, "next": {}, "title": {},
	"break": {}, "caption": {}, "insert": {}, "vocal": {}, "lyrics": {},
	"lyric": {}, "sound": {}, "music": {},
}

// --- small helpers ---------------------------------------------------------

func splitTrim(s, sep string) []string {
	raw := strings.Split(s, sep)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func minMax(a, b int) (int, int) {
	if a < b {
		return a, b
	}
	return b, a
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
