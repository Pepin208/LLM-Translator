package engine

import (
	"fmt"
	"sort"
	"strings"

	"translate_llm/internal/config"
)

// --- Overlap context -------------------------------------------------------

// GetOverlapContext returns prior translated lines for sequential consistency.
// numLines < 0 selects the automatic policy.
func GetOverlapContext(currentStart int, dialogues []Pair, s *config.TranslationSession, numLines int) []string {
	if numLines < 0 {
		if currentStart < 3 {
			numLines = currentStart
		} else {
			end := currentStart + 10
			if end > len(dialogues) {
				end = len(dialogues)
			}
			sample := dialogues[currentStart:end]
			avg := 0.0
			if len(sample) > 0 {
				total := 0
				for _, d := range sample {
					total += len(d.Text)
				}
				avg = float64(total) / float64(len(sample))
			}
			if avg < 45 {
				numLines = 3
			} else {
				numLines = s.ContextLines
			}
		}
	}

	var contextLines []string
	for i := currentStart - 1; i >= 0; i-- {
		key := s.CacheKey(dialogues[i].ID, dialogues[i].Text)
		translated, ok := s.Cache[key]
		if !ok {
			continue
		}
		sanitized := SanitizeLineBreaks(translated)
		if name := dialogues[i].Name; name != "" {
			contextLines = append(contextLines, name+": "+sanitized)
		} else {
			contextLines = append(contextLines, sanitized)
		}
		if len(contextLines) == numLines {
			break
		}
	}
	// reverse
	for i, j := 0, len(contextLines)-1; i < j; i, j = i+1, j-1 {
		contextLines[i], contextLines[j] = contextLines[j], contextLines[i]
	}
	return contextLines
}

// --- Payload ---------------------------------------------------------------

func buildTranslationPayload(pairs []Pair, overlapLines []string, s *config.TranslationSession) string {
	var lines []string
	for _, text := range overlapLines {
		lines = append(lines, "[C]: "+text)
	}
	ids := map[int]struct{}{}
	for _, p := range pairs {
		sanitized := SanitizeLineBreaks(p.Text)
		if p.Name != "" {
			lines = append(lines, fmt.Sprintf("[%d] %s: %s", p.ID, p.Name, sanitized))
		} else {
			lines = append(lines, fmt.Sprintf("[%d]: %s", p.ID, sanitized))
		}
		ids[p.ID] = struct{}{}
	}
	// Hints are emitted in a deterministic order.
	hintIDs := make([]int, 0, len(s.RetryHints))
	for id := range s.RetryHints {
		if _, ok := ids[id]; ok {
			hintIDs = append(hintIDs, id)
		}
	}
	sort.Ints(hintIDs)
	for _, id := range hintIDs {
		lines = append(lines, "[HINT]: "+s.RetryHints[id])
	}
	return strings.Join(lines, "\n")
}
