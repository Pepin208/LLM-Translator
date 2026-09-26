package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/engine"
	"github.com/Pepin208/LLM-Translator/internal/finance"
	"github.com/Pepin208/LLM-Translator/internal/utils"
)

// --- SSE -------------------------------------------------------------------

func (s *Server) handleStreamLogs(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.broker.Subscribe()
	defer s.broker.Unsubscribe(ch)
	s.logger.Debug("SSE client connected", "ip", clientIP(req))

	fmt.Fprint(w, "data: [Connected to LLM Translator Log Stream]\n\n")
	flusher.Flush()

	for {
		select {
		case <-req.Context().Done():
			s.logger.Debug("SSE client disconnected", "ip", clientIP(req))
			return
		case msg := <-ch:
			for _, line := range strings.Split(strings.TrimRight(msg, "\n"), "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
		}
	}
}

// --- Projection / translation ---------------------------------------------

func (s *Server) handleProject(w http.ResponseWriter, req *http.Request) {
	var data map[string]any
	if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
		s.logger.Warn("project rejected: invalid JSON", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid JSON body."})
		return
	}
	session, filePaths, excluded, err := s.buildSession(data)
	if err != nil {
		s.logger.Warn("project rejected", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	s.logger.Info("projection requested",
		"files", len(filePaths), "model", session.ModelID,
		"provider", session.ActiveProvider, "source", session.SourceLang,
		"target", session.TargetLang, "currency", session.TargetCurrency)

	var exchangeRate *decimal.Decimal
	if session.ActiveProvider == "OpenRouter" && session.APIKey != "" {
		_, exchangeRate = finance.CheckOpenRouterBalance(session.APIKey, session.TargetCurrency, session.Out)
	}

	var buf strings.Builder
	session.Out = &buf
	var queueCount int
	var totalIdeal, totalWorst float64
	func() {
		defer func() { _ = recover() }()
		queue, ideal, worst := engine.BuildExecutionQueue(filePaths, excluded, session, exchangeRate, session.TargetCurrency)
		finance.DisplayGrandTotalProjection(ideal, worst, exchangeRate, session.TargetCurrency, &buf)
		queueCount = len(queue)
		totalIdeal, totalWorst = ideal, worst
	}()

	captured := buf.String()
	s.broker.Broadcast(captured)
	s.logger.Info("projection ready",
		"queue_count", queueCount, "ideal_usd", totalIdeal, "worst_usd", totalWorst)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "success",
		"queue_count":     queueCount,
		"total_ideal_usd": totalIdeal,
		"total_worst_usd": totalWorst,
		"target_currency": session.TargetCurrency,
		"prompt_cost":     session.PromptCost,
		"completion_cost": session.CompletionCost,
		"logs":            captured,
	})
}

func (s *Server) handleTranslate(w http.ResponseWriter, req *http.Request) {
	var data map[string]any
	if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
		s.logger.Warn("translate rejected: invalid JSON", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid JSON body."})
		return
	}

	s.translationMu.Lock()
	if s.translationActive {
		s.translationMu.Unlock()
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Translation is already running."})
		return
	}

	session, filePaths, excluded, err := s.buildSession(data)
	if err != nil {
		s.translationMu.Unlock()
		s.logger.Warn("translate rejected", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	// Only the actual translation run pays for the glossary enrichment call.
	session.EnrichGlossary = true

	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	s.translationActive = true
	s.translationMu.Unlock()

	s.logger.Info("translation started",
		"files", len(filePaths), "model", session.ModelID,
		"provider", session.ActiveProvider, "source", session.SourceLang,
		"target", session.TargetLang, "currency", session.TargetCurrency,
		"batch_size", session.BatchSize, "context_mode", session.ContextMode,
		"reasoning_effort", session.ReasoningEffort, "ip", clientIP(req))

	go func() {
		defer func() {
			s.translationMu.Lock()
			s.translationActive = false
			s.translationMu.Unlock()
			cancel()
		}()

		writer := broadcastWriter{broker: s.broker}
		session.Out = writer

		var exchangeRate *decimal.Decimal
		if session.ActiveProvider == "OpenRouter" && session.APIKey != "" {
			if usd, rate := finance.CheckOpenRouterBalance(session.APIKey, session.TargetCurrency, writer); usd != nil {
				session.InitialUSDBalance = *usd
				exchangeRate = rate
			}
		}

		queue, _, _ := engine.BuildExecutionQueue(filePaths, excluded, session, exchangeRate, session.TargetCurrency)
		s.broker.Broadcast("\n=== STARTING TRANSLATION ===\n")
		engine.ExecuteTranslationQueue(ctx, queue, session, exchangeRate, session.TargetCurrency, len(filePaths), s.outputDir)
		if ctx.Err() != nil {
			s.logger.Info("translation cancelled", "files", len(filePaths))
			s.broker.Broadcast("\n=== TRANSLATION CANCELLED ===\n")
		} else {
			s.logger.Info("translation completed",
				"files", len(filePaths), "prompt_tokens", session.TotalCachedTokens)
			s.broker.Broadcast("\n=== TRANSLATION COMPLETED ===\n")
		}
	}()

	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "file_count": len(filePaths)})
}

func (s *Server) handleCancel(w http.ResponseWriter, req *http.Request) {
	s.translationMu.Lock()
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	s.translationMu.Unlock()
	s.logger.Info("cancellation requested", "ip", clientIP(req))
	s.broker.Broadcast("\n[CANCEL] Stop signal sent to worker thread...\n")
	writeJSON(w, http.StatusOK, map[string]any{"status": "cancelled"})
}

// buildSession assembles a TranslationSession from a request payload.
func (s *Server) buildSession(data map[string]any) (*config.TranslationSession, []string, [][2]int, error) {
	var filePaths []string
	if raw, ok := data["file_paths"].([]any); ok {
		for _, p := range raw {
			if s, ok := p.(string); ok {
				filePaths = append(filePaths, s)
			}
		}
	}
	if len(filePaths) == 0 {
		return nil, nil, nil, fmt.Errorf("no files specified")
	}
	modelID, _ := data["model_id"].(string)
	if modelID == "" {
		return nil, nil, nil, fmt.Errorf("no model selected")
	}

	sourceLang := strDefault(data, "source_lang", "EN")
	targetLang := strDefault(data, "target_lang", "ES")
	targetCurrency := strDefault(data, "target_currency", "USD")
	domainContext, _ := data["domain_context"].(string)
	excluded := utils.ParseExcludedRangesStr(strDefault(data, "excluded_ranges", ""))

	pCost := asFloat(data["prompt_cost"])
	cCost := asFloat(data["completion_cost"])
	ctxLen := asInt(data["context_length"])
	if pCost == 0 && cCost == 0 {
		fp, fc, fctx := s.getModelPricing(modelID)
		pCost, cCost = fp, fc
		if ctxLen == 0 {
			ctxLen = fctx
		}
	}
	if ctxLen == 0 {
		ctxLen = config.DefaultContextLength
	}

	cfg := config.LoadConfig()
	activeProv := sanitizeProvider(cfg["active_provider"])
	apiKey := config.GetAPIKey(activeProv, cfg)
	localURL, _ := cfg["local_server_url"].(string)

	session := config.NewSession()
	session.APIKey = apiKey
	session.ModelID = modelID
	session.PromptCost = pCost
	session.CompletionCost = cCost
	session.ContextLength = ctxLen
	session.SourceLang = sourceLang
	session.TargetLang = targetLang
	session.DomainContext = domainContext
	session.DynamicSystemPrompt = config.BuildSystemPrompt(sourceLang, targetLang, modelID, domainContext)
	session.TargetCurrency = targetCurrency
	session.ActiveProvider = activeProv
	session.LocalServerURL = localURL
	session.ProviderSort = strDefault(data, "provider_sort", "price")
	session.SelectedProvider, _ = data["selected_provider"].(string)
	if allow, ok := data["allow_training"].(bool); ok {
		session.AllowTraining = allow
	}

	// Reasoning + batch/context controls.
	switch strDefault(data, "reasoning_effort", "auto") {
	case "none", "low", "medium", "high":
		session.ReasoningEffort = strDefault(data, "reasoning_effort", "auto")
	default:
		session.ReasoningEffort = "auto"
	}
	session.ModelSupportsReasoning, _ = data["model_supports_reasoning"].(bool)
	if v := asInt(data["batch_size"]); v > 0 {
		session.BatchSize = v
	}
	if v := asInt(data["context_lines"]); v > 0 {
		session.ContextLines = v
	}
	switch strDefault(data, "context_mode", "") {
	case "overlap", "briefing", "full":
		session.ContextMode = strDefault(data, "context_mode", "overlap")
	}

	session.GlossaryDir = filepath.Join(config.BASE_DIR, "web_storage", "glossary")
	_ = os.MkdirAll(session.GlossaryDir, 0o755)
	return session, filePaths, excluded, nil
}
