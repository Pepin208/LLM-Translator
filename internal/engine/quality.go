package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/utils"
)

// --- Quality checks --------------------------------------------------------

var (
	nonWordSpace = regexp.MustCompile(`[^\p{L}\p{N}_\s]`)
	tagReplace   = regexp.MustCompile(`<t\d+/>|<br/>|<nbr/>`)
	digitsRe     = regexp.MustCompile(`\d+`)
)

func normForEcho(s string) string {
	s = tagReplace.ReplaceAllString(s, " ")
	s = nonWordSpace.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	return strings.TrimSpace(s)
}

func qualityCheckEcho(val, original string, s *config.TranslationSession, glossary []string) string {
	_, romanized := config.RomanizedSourceLangs[s.SourceLang]
	sourceIsLatin := !romanized

	normOrig := normForEcho(original)
	normVal := normForEcho(val)
	origWords := strings.Fields(normOrig)

	if !sourceIsLatin || len(origWords) < 2 || normOrig != normVal || !hasAlpha(normVal) {
		return ""
	}

	glossaryKeys := map[string]struct{}{}
	for _, n := range glossary {
		norm := utils.NormalizeNameKey(n)
		glossaryKeys[norm] = struct{}{}
		for _, w := range strings.Fields(norm) {
			glossaryKeys[w] = struct{}{}
		}
	}

	whitelisted := true
	for _, w := range origWords {
		if isAllDigitsStr(w) {
			continue
		}
		if _, ok := config.IdentityWhitelist[utils.NormalizeNameKey(w)]; ok {
			continue
		}
		if _, ok := glossaryKeys[utils.NormalizeNameKey(w)]; ok {
			continue
		}
		whitelisted = false
		break
	}
	if whitelisted {
		return ""
	}
	return fmt.Sprintf("The previous translation '%s' was not translated (echo of the source). Retranslate it into %s.", val, s.TargetLang)
}

func qualityCheckArtefacts(val, original string, s *config.TranslationSession) (bool, string) {
	valPlain := strings.ReplaceAll(strings.ReplaceAll(val, "<br/>", " "), "<nbr/>", " ")
	lowered := strings.ToLower(valPlain)
	if s.SourceLang == "EN" {
		for _, idiom := range config.EchoIdioms {
			if strings.Contains(lowered, idiom) {
				return true, fmt.Sprintf("The previous translation '%s' was a literal calque of English. Use a natural equivalent Spanish expression.", val)
			}
		}
	}
	if strings.Count(val, "{") != strings.Count(val, "}") {
		return true, fmt.Sprintf("The previous translation '%s' has unbalanced braces. Preserve the exact tags and placeholders.", val)
	}
	if len(val) < 2 && len(original) > 3 {
		return true, fmt.Sprintf("The previous translation '%s' was truncated. Translate the full line.", val)
	}
	return false, ""
}

var numberWordMap = map[string][]string{
	"1": {"1", "uno", "una", "primero", "primera", "número", "nº", "n°"},
	"2": {"2", "dos", "segundo", "segunda", "número", "nº", "n°"},
	"3": {"3", "tres", "tercero", "tercera", "número", "nº", "n°"},
	"4": {"4", "cuatro", "cuarto", "cuarta"},
	"5": {"5", "cinco", "quinto", "quinta"},
}

func qualityCheckAnchors(val, original string, glossary []string) (bool, string) {
	srcPlain := tagReplace.ReplaceAllString(original, " ")
	srcNK := utils.NormalizeNameKey(srcPlain)

	anchors := map[string]struct{}{}
	for _, d := range digitsRe.FindAllString(srcNK, -1) {
		anchors[d] = struct{}{}
	}
	for _, g := range glossary {
		ng := utils.NormalizeNameKey(g)
		if ng != "" && strings.Contains(srcNK, ng) {
			anchors[ng] = struct{}{}
		}
	}
	if len(anchors) == 0 {
		return false, ""
	}

	valNK := utils.NormalizeNameKey(strings.ReplaceAll(strings.ReplaceAll(val, "<br/>", " "), "<nbr/>", " "))
	for a := range anchors {
		if strings.Contains(valNK, a) {
			return false, ""
		}
		if words, ok := numberWordMap[a]; ok {
			for _, w := range words {
				if strings.Contains(valNK, w) {
					return false, ""
				}
			}
		}
	}
	keys := make([]string, 0, len(anchors))
	for a := range anchors {
		keys = append(keys, a)
	}
	sort.Strings(keys)
	return true, fmt.Sprintf("The previous translation '%s' does not correspond to this line. Keep every number and proper name exactly; translate only the text of this exact line.", val)
}
