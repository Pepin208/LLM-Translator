// Command cli is the interactive command-line interface for the Go port.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"translate_llm/internal/config"
	"translate_llm/internal/engine"
	"translate_llm/internal/finance"
	"translate_llm/internal/utils"
)

var (
	flagProvider string
	flagModel    string
	flagSource   string
	flagTarget   string
	flagCurrency string
	flagFiles    string
	flagDomain   string
	flagExclude  string
	flagYes      bool
)

func main() {
	root := &cobra.Command{
		Use:   "llmt",
		Short: "LLM Subtitle Translator (Go) — interactive CLI",
		RunE:  run,
	}
	root.Flags().StringVar(&flagProvider, "provider", "", "Provider name")
	root.Flags().StringVar(&flagModel, "model", "", "Model ID")
	root.Flags().StringVar(&flagSource, "source", "", "Source language code")
	root.Flags().StringVar(&flagTarget, "target", "", "Target language code")
	root.Flags().StringVar(&flagCurrency, "currency", "", "Display currency")
	root.Flags().StringVar(&flagFiles, "files", "", "Subtitle file or directory")
	root.Flags().StringVar(&flagDomain, "domain", "", "Optional domain context")
	root.Flags().StringVar(&flagExclude, "exclude", "", "Excluded line ranges, e.g. 1-15,202")
	root.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation prompt")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, _ []string) error {
	cfg := config.LoadConfig()
	interactive := flagProvider == "" || flagModel == "" || flagFiles == ""

	provider := firstNonEmpty(flagProvider, str(cfg["active_provider"]), "OpenRouter")
	if !validProvider(provider) {
		return fmt.Errorf("unknown provider %q", provider)
	}

	apiKey := config.GetAPIKey(provider, cfg)
	if apiKey == "" && provider != "Local" {
		if err := huh.NewInput().Title(fmt.Sprintf("API key for %s", provider)).Value(&apiKey).Run(); err != nil {
			return err
		}
		if apiKey != "" {
			cfg[strings.ToLower(strings.ReplaceAll(provider, " ", "_"))+"_api_key"] = apiKey
			_ = config.SaveConfig(cfg)
		}
	}

	model := firstNonEmpty(flagModel, str(cfg["model_id"]))
	if model == "" {
		if err := huh.NewInput().Title("Model ID").Value(&model).Run(); err != nil {
			return err
		}
	}
	source := firstNonEmpty(flagSource, str(cfg["source_lang"]), "EN")
	target := firstNonEmpty(flagTarget, str(cfg["target_lang"]), "ES")
	if interactive && flagSource == "" {
		_ = selectLanguage("Select source language", &source)
	}
	if interactive && flagTarget == "" {
		_ = selectLanguage("Select target language", &target)
	}
	currency := firstNonEmpty(flagCurrency, str(cfg["target_currency"]), "USD")

	filesArg := flagFiles
	if filesArg == "" {
		if err := huh.NewInput().Title("Subtitle file or folder").Value(&filesArg).Run(); err != nil {
			return err
		}
	}
	targetFiles, err := resolveFiles(filesArg)
	if err != nil {
		return err
	}

	domain := flagDomain
	if domain == "" {
		domain = str(cfg["domain_context"])
	}

	cfg["active_provider"] = provider
	cfg["model_id"] = model
	cfg["source_lang"] = source
	cfg["target_lang"] = target
	cfg["target_currency"] = currency
	_ = config.SaveConfig(cfg)

	localURL := str(cfg["local_server_url"])
	session := config.NewSession()
	session.APIKey = apiKey
	session.ModelID = model
	session.PromptCost = 0
	session.CompletionCost = 0
	session.ContextLength = 128000
	session.SourceLang = source
	session.TargetLang = target
	session.DomainContext = domain
	session.DynamicSystemPrompt = config.BuildSystemPrompt(source, target, model, domain)
	session.TargetCurrency = currency
	session.ActiveProvider = provider
	session.LocalServerURL = localURL
	session.GlossaryDir = filepath.Join(config.BASE_DIR, "web_storage", "glossary")
	session.EnrichGlossary = true

	// Balance and projection.
	balance, rate := finance.CheckOpenRouterBalance(apiKey, currency, os.Stdout)
	if balance != nil {
		session.InitialUSDBalance = *balance
	}

	excluded := parseExclude(flagExclude)
	_, ideal, worst := engine.BuildExecutionQueue(targetFiles, excluded, session, rate, currency)
	finance.DisplayGrandTotalProjection(ideal, worst, rate, currency, os.Stdout)

	if !flagYes {
		var proceed bool = true
		if err := huh.NewConfirm().Title("Proceed with translation?").Affirmative("Yes").Negative("No").Value(&proceed).Run(); err != nil {
			return err
		}
		if !proceed {
			fmt.Println("Translation cancelled by user.")
			return nil
		}
	}

	queue, _, _ := engine.BuildExecutionQueue(targetFiles, excluded, session, rate, currency)
	engine.ExecuteTranslationQueue(context.Background(), queue, session, rate, currency, len(targetFiles), "")
	return nil
}

func selectLanguage(title string, value *string) error {
	options := make([]huh.Option[string], 0, len(config.LanguageChoices))
	for _, l := range config.LanguageChoices {
		options = append(options, huh.NewOption(fmt.Sprintf("%s - %s", l.Code, l.Name), l.Code))
	}
	return huh.NewSelect[string]().Title(title).Options(options...).Value(value).Run()
}

func resolveFiles(input string) ([]string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, fmt.Errorf("no input path provided")
	}
	input = filepath.Clean(input)

	info, err := os.Stat(input)
	if err != nil {
		return nil, fmt.Errorf("file or directory %q does not exist", input)
	}

	if !info.IsDir() {
		ext := strings.ToLower(filepath.Ext(input))
		if ext != ".srt" && ext != ".ass" && ext != ".vtt" {
			return nil, fmt.Errorf("unsupported file extension %q", ext)
		}
		return []string{input}, nil
	}

	var found []string
	err = filepath.WalkDir(input, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".srt", ".ass", ".vtt":
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no subtitle files found in %q", input)
	}
	return found, nil
}

func parseExclude(raw string) [][2]int {
	if raw == "" {
		return nil
	}
	return utils.ParseExcludedRangesStr(raw)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func validProvider(name string) bool {
	for _, p := range config.ValidProviders {
		if p == name {
			return true
		}
	}
	return false
}
