// Package config centralizes constants, domain pricing maps and the shared
// TranslationSession state, mirroring the Python core/config.py module.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/shopspring/decimal"
)

// BASE_DIR is the directory holding translator_config.json and web_storage/.
// Resolution order:
//  1. $TRANSLATOR_BASE (explicit override)
//  2. the nearest ancestor of the working directory containing go.mod (dev checkout)
//  3. the directory of the running executable (portable release binary)
var BASE_DIR = resolveBaseDir()

func resolveBaseDir() string {
	if v := strings.TrimSpace(os.Getenv("TRANSLATOR_BASE")); v != "" {
		return v
	}
	if cwd, err := os.Getwd(); err == nil {
		if root, ok := findUp(cwd, "go.mod"); ok {
			return root
		}
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		return filepath.Dir(exe)
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// findUp walks from start towards the filesystem root looking for name.
func findUp(start, name string) (string, bool) {
	for d := start; ; {
		if _, err := os.Stat(filepath.Join(d, name)); err == nil {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

var (
	ConfigPath = resolveConfigPath(BASE_DIR)
	LogPath    = filepath.Join(BASE_DIR, "translator_log.txt")
)

// resolveConfigPath prefers a local translator_config.json (often a symlink to
// the shared Python config) so it is visible inside Port_Go, then the parent
// project root, then the local directory.
func resolveConfigPath(base string) string {
	if env := os.Getenv("TRANSLATOR_CONFIG"); env != "" {
		return env
	}
	local := filepath.Join(base, "translator_config.json")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	parent := filepath.Join(filepath.Dir(base), "translator_config.json")
	if _, err := os.Stat(parent); err == nil {
		return parent
	}
	return local
}

// --- Regexes ---------------------------------------------------------------

var (
	ASSTagRegex = regexp.MustCompile(`(\{.*?\})`)
	VTTTagRegex = regexp.MustCompile(`(</?b>|</?i>|</?u>|<v [^>]+>|</v>|<lang [^>]+>|</lang>|<ruby>|</ruby>)`)
	TagPattern  = regexp.MustCompile(`<t\d+/>|<br/>|<nbr/>`)
)

// ExcludedStylePatterns are matched case-insensitively against dialogue styles.
var ExcludedStylePatterns = []string{
	"op", "ed", "title", "break", "next episode title", "next time",
	"sign", "insert", "caption", "dialogue2",
}

// LanguageChoices maps ISO codes to display names, preserving order.
type LanguageChoice struct {
	Code string
	Name string
}

var LanguageChoices = []LanguageChoice{
	{"EN", "English"}, {"ES", "Spanish"}, {"JA", "Japanese"},
	{"ZH", "Chinese"}, {"KO", "Korean"}, {"FR", "French"},
	{"DE", "German"}, {"PT", "Portuguese"}, {"IT", "Italian"},
	{"RU", "Russian"}, {"TR", "Turkish"}, {"AR", "Arabic"},
}

// GlossaryOverrides can be mutated at runtime by user-specific configuration.
var GlossaryOverrides = map[string][]string{}

var TranslationModelMarkers = []string{
	"hy-mt2", "hymt2", "nllb", "opus-mt", "helsinki-nlp", "seamless",
	"madlad", "tower", "alma-", "bayling",
}

// KnownCosts maps model_id -> {prompt_cost, completion_cost} per token.
var KnownCosts = map[string][2]float64{
	"gpt-4o":                     {0.0000025, 0.0000100},
	"gpt-4o-mini":                {0.00000015, 0.0000006},
	"o1":                         {0.000015, 0.000060},
	"o1-mini":                    {0.000003, 0.000012},
	"o3-mini":                    {0.0000011, 0.0000044},
	"claude-3-5-sonnet-20241022": {0.000003, 0.000015},
	"claude-3-5-haiku-20241022":  {0.000001, 0.000005},
	"deepseek-chat":              {0.00000027, 0.00000110},
	"deepseek-reasoner":          {0.00000055, 0.00000219},
	"gemini-2.0-flash":           {0.00000010, 0.00000040},
	"gemini-1.5-pro":             {0.00000125, 0.00000500},
}

// ProviderModels lists offline fallbacks per provider.
var ProviderModels = map[string][]string{
	"OpenAI":        {"gpt-4o", "gpt-4o-mini", "o1", "o1-mini", "o3-mini"},
	"Anthropic":     {"claude-3-5-sonnet-20241022", "claude-3-5-haiku-20241022", "claude-3-opus-20240229"},
	"DeepSeek":      {"deepseek-chat", "deepseek-reasoner"},
	"Google Gemini": {"gemini-2.0-flash", "gemini-1.5-flash", "gemini-1.5-pro"},
	"Local":         {"default"},
}

// OpenRouterModelFallback is used when the live catalogue cannot be fetched.
var OpenRouterModelFallback = []string{
	"deepseek/deepseek-chat",
	"google/gemini-2.0-flash-001",
	"meta-llama/llama-3.3-70b-instruct",
	"openai/gpt-4o-mini",
	"anthropic/claude-3.5-haiku",
	"qwen/qwen-2.5-72b-instruct",
	"mistralai/mistral-large-2411",
}

// OpenCodeZenModels is the offline fallback for the OpenCode Zen/Go gateways.
var OpenCodeZenModels = []string{
	"deepseek-v4-flash",
	"deepseek-v4-pro",
	"glm-5.2",
	"glm-5.1",
	"kimi-k2.5",
	"minimax-m3",
}

// IdentityWhitelist (interjections, common titles, numbers).
var IdentityWhitelist = map[string]struct{}{
	"ok": {}, "okay": {}, "ah": {}, "oh": {}, "ha": {}, "haha": {}, "hahaha": {},
	"hey": {}, "hmm": {}, "uh": {}, "um": {}, "dr": {}, "mr": {}, "mrs": {},
	"ms": {}, "general": {}, "captain": {}, "commander": {}, "admiral": {},
	"prof": {}, "professor": {}, "president": {}, "nasa": {},
}

var RomanizedSourceLangs = map[string]struct{}{"JA": {}, "ZH": {}, "KO": {}}

var EchoIdioms = []string{
	"sore thumb", "give it a rest", "no way", "that's a given", "out of the blue",
	"a piece of cake", "raining cats and dogs", "once in a blue moon",
	"hit the sack", "break a leg", "over the moon", "under the weather",
	"spill the beans", "blessing in disguise", "better late than never",
}

// LangCharFactors approximates characters-per-token per language.
var LangCharFactors = map[string]float64{
	"EN": 4.0, "FR": 3.8, "PT": 3.8, "IT": 3.8, "ES": 3.5, "DE": 3.2,
	"TR": 3.2, "RU": 2.5, "AR": 2.5, "KO": 2.0, "JA": 1.8, "ZH": 1.8,
}

const (
	MaxRetries      = 3
	RequestTimeout  = 90
	RetrySleep      = 5.0
	ChunkSize       = 100
	MaxRetryBatch   = 20
	MinRequestDelay = 0.5

	ContextLines         = 5
	FallbackContextLines = 2
	CacheSaveInterval    = 5
)

// ValidProviders is the canonical provider name set.
var ValidProviders = []string{
	"OpenRouter", "OpenAI", "Anthropic", "DeepSeek", "Google Gemini",
	"Local", "OpenCode Zen", "OpenCode Go",
}

// --- Session ---------------------------------------------------------------

// TranslationSession encapsulates the state and configuration of a translation run.
type TranslationSession struct {
	APIKey              string
	ModelID             string
	PromptCost          float64
	CompletionCost      float64
	ContextLength       int
	SourceLang          string
	TargetLang          string
	DomainContext       string
	DynamicSystemPrompt string
	// ContextBlock is a per-file stable block (briefing or full subtitle) that
	// providers append after the system prompt, before the volatile batch.
	ContextBlock   string
	CacheFilepath  string
	TargetCurrency string

	Cache      map[string]string
	RetryHints map[int]string

	ActiveProvider   string
	ProviderInstance any
	ProviderSort     string
	AllowTraining    bool
	SelectedProvider string
	LocalServerURL   string

	GlossaryNames     []string
	LastCachedTokens  int
	TotalCachedTokens int

	// ReasoningEffort: auto|none|low|medium|high. ModelSupportsReasoning is
	// filled from the provider catalogue so "auto" can decide.
	ReasoningEffort        string
	ModelSupportsReasoning bool

	// Batch/context controls (configurable from the UI).
	BatchSize    int
	ContextLines int
	ContextMode  string // overlap|briefing|full

	// GlossaryDir is where per-series glossaries are persisted
	// (e.g. web_storage/glossary). EnrichGlossary enables the optional LLM pass
	// that extracts genre/locations/terms (skipped for cost projections).
	GlossaryDir    string
	EnrichGlossary bool

	InitialUSDBalance decimal.Decimal

	// CacheWriteCounter throttles full cache flushes (see engine).
	CacheWriteCounter int

	// Out receives progress output. Replaces the Python global stdout swap.
	Out io.Writer
}

// NewSession builds a session with sane defaults.
func NewSession() *TranslationSession {
	return &TranslationSession{
		Cache:             map[string]string{},
		RetryHints:        map[int]string{},
		ProviderSort:      "price",
		AllowTraining:     true,
		InitialUSDBalance: decimal.Zero,
		Out:               os.Stdout,
		ReasoningEffort:   "auto",
		BatchSize:         ChunkSize,
		ContextLines:      ContextLines,
		ContextMode:       "overlap",
	}
}

// CacheKey builds the canonical cache key for a dialogue line.
func (s *TranslationSession) CacheKey(lineID int, sourceText string) string {
	return fmt.Sprintf("%s:%d:%s", s.TargetLang, lineID, sourceText)
}

// --- Config file I/O -------------------------------------------------------

// LoadConfig reads translator_config.json into a generic map so unknown keys
// (gui_*, auth_*) are preserved on save.
func LoadConfig() map[string]any {
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// SaveConfig writes the config atomically via a temporary file.
func SaveConfig(cfg map[string]any) error {
	return atomicWriteJSON(ConfigPath, cfg)
}

// SaveCache writes a translation cache atomically.
func SaveCache(cache map[string]string, path string) error {
	if path == "" {
		return nil
	}
	return atomicWriteJSON(path, cache)
}

// LoadCache reads a translation cache from disk.
func LoadCache(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]string{}
	}
	return out
}

var atomicWriteMu sync.Mutex

func atomicWriteJSON(path string, value any) error {
	atomicWriteMu.Lock()
	defer atomicWriteMu.Unlock()

	// If the path is a symlink (e.g. shared with the Python version), resolve it
	// so the rename updates the target file instead of replacing the link.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	dir := filepath.Dir(path)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// --- Helpers ---------------------------------------------------------------

// GetAPIKey prioritizes the environment variable {PROVIDER}_API_KEY over config.
// OpenCode Zen and Go share a single key (opencode_api_key / OPENCODE_API_KEY).
func GetAPIKey(providerName string, cfg map[string]any) string {
	if providerName == "OpenCode Zen" || providerName == "OpenCode Go" {
		if v := strings.TrimSpace(os.Getenv("OPENCODE_API_KEY")); v != "" {
			return v
		}
		if v, ok := cfg["opencode_api_key"].(string); ok {
			return v
		}
		return ""
	}
	envKey := strings.ToUpper(strings.ReplaceAll(providerName, " ", "_")) + "_API_KEY"
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	cfgKey := strings.ToLower(strings.ReplaceAll(providerName, " ", "_")) + "_api_key"
	if v, ok := cfg[cfgKey].(string); ok {
		return v
	}
	return ""
}

// BuildSystemPrompt builds the centralized translation system prompt.
func BuildSystemPrompt(sourceLang, targetLang, modelID, domainContext string) string {
	spanishRule := ""
	if strings.HasPrefix(strings.ToUpper(targetLang), "ES") {
		spanishRule = " strictly use Neutral Latin American Spanish (no 'vosotros', no 'voseo'). " +
			"Prohibit Peninsular/Spain slang (e.g. do not use 'petar', 'molar', 'chaval', 'guay', 'curro', 'pasta'). " +
			"Mandatorily use inverted question (¿) and exclamation (¡) marks. " +
			"Apply correct grammatical gender agreement (articles, adjectives, pronouns) using the series glossary when provided."
	}

	var prompt string
	if IsTranslationModel(modelID) {
		prompt = fmt.Sprintf(
			"Translate %s to %s.%s You are a subtitle translation engine. Output one line per subtitle using format 'ID|Translated Text'. Always translate the line; never return the source text unchanged.",
			sourceLang, targetLang, spanishRule,
		)
	} else {
		prompt = fmt.Sprintf(
			"Translate %s to %s.%s Strict rules:\n"+
				"1. Output format: Exactly one line per input line using 'ID|Translated Text'. Do not merge or split lines.\n"+
				"2. Preserve all XML tags like <t0/>, <br/>, <nbr/> exactly in position.\n"+
				"3. Do not add explanations or conversational responses.\n"+
				"4. Translate the whole line; never return the source text unchanged.",
			sourceLang, targetLang, spanishRule,
		)
	}
	if domainContext != "" {
		prompt += " Context: " + domainContext
	}
	return prompt
}

// IsStyleExcluded reports whether a dialogue style should be skipped.
func IsStyleExcluded(style string) bool {
	if style == "" {
		return false
	}
	lowered := strings.ToLower(style)
	for _, pat := range ExcludedStylePatterns {
		if strings.Contains(lowered, pat) {
			return true
		}
	}
	return false
}

// IsTranslationModel reports whether modelID looks like a dedicated MT model.
func IsTranslationModel(modelID string) bool {
	lower := strings.ToLower(modelID)
	for _, marker := range TranslationModelMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
