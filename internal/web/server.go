package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"translate_llm/internal/config"
	"translate_llm/internal/engine"
	"translate_llm/internal/finance"
	"translate_llm/internal/utils"
)

const (
	defaultPort = 21346

	maxUploadFiles = 50
	maxUploadBytes = 25 * 1024 * 1024

	loginMaxAttempts = 5
	loginWindow      = 60 * time.Second
)

// Broker fans out log lines to all connected SSE subscribers.
type Broker struct {
	mu   sync.Mutex
	subs map[chan string]struct{}
}

// NewBroker creates an empty broker.
func NewBroker() *Broker {
	return &Broker{subs: map[chan string]struct{}{}}
}

// Subscribe registers a new subscriber channel.
func (b *Broker) Subscribe() chan string {
	ch := make(chan string, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (b *Broker) Unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

// Broadcast sends a message to every subscriber without blocking.
func (b *Broker) Broadcast(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

type broadcastWriter struct{ broker *Broker }

func (w broadcastWriter) Write(p []byte) (int, error) {
	w.broker.Broadcast(string(p))
	return len(p), nil
}

// Server holds the HTTP server state.
type Server struct {
	baseDir   string
	staticDir string
	uploadDir string
	outputDir string
	port      int

	broker *Broker

	translationMu     sync.Mutex
	translationActive bool
	cancelFunc        context.CancelFunc

	loginMu       sync.Mutex
	loginAttempts map[string][]time.Time

	modelsMu    sync.Mutex
	modelsCache []map[string]any
	modelsTime  time.Time
}

// NewServer builds a server rooted at baseDir.
func NewServer(baseDir string) *Server {
	uploadDir := filepath.Join(baseDir, "web_storage", "uploads")
	outputDir := filepath.Join(baseDir, "web_storage", "outputs")
	_ = os.MkdirAll(uploadDir, 0o755)
	_ = os.MkdirAll(outputDir, 0o755)
	return &Server{
		baseDir:       baseDir,
		staticDir:     filepath.Join(baseDir, "static"),
		uploadDir:     uploadDir,
		outputDir:     outputDir,
		port:          defaultPort,
		broker:        NewBroker(),
		loginAttempts: map[string][]time.Time{},
	}
}

// UseSSL reports whether cert.pem/key.pem exist in the project root.
func (s *Server) UseSSL() bool {
	_, e1 := os.Stat(filepath.Join(s.baseDir, "cert.pem"))
	_, e2 := os.Stat(filepath.Join(s.baseDir, "key.pem"))
	return e1 == nil && e2 == nil
}

// Router builds the HTTP handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.authMiddleware)

	r.Post("/api/login", s.handleLogin)
	r.Post("/api/logout", s.handleLogout)

	r.Get("/api/info", s.handleInfo)
	r.Get("/api/currencies", s.handleCurrencies)
	r.Get("/api/languages", s.handleLanguages)
	r.Get("/api/config", s.handleGetConfig)
	r.Post("/api/config", s.handleUpdateConfig)
	r.Get("/api/models", s.handleModels)
	r.Get("/api/model-endpoints", s.handleModelEndpoints)
	r.Post("/api/upload", s.handleUpload)
	r.Get("/api/stream-logs", s.handleStreamLogs)
	r.Post("/api/project", s.handleProject)
	r.Post("/api/translate", s.handleTranslate)
	r.Post("/api/cancel", s.handleCancel)
	r.Get("/api/download/{filename}", s.handleDownload)

	r.Get("/favicon.ico", s.handleFavicon)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(s.staticDir))))
	r.Get("/", s.handleIndex)
	return r
}

// --- Auth ------------------------------------------------------------------

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if strings.HasPrefix(path, "/api/") && path != "/api/login" {
			if !IsAuthenticated(req) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Authentication required."})
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, req *http.Request) {
	ip := clientIP(req)
	now := time.Now()

	s.loginMu.Lock()
	attempts := s.loginAttempts[ip]
	fresh := attempts[:0]
	for _, t := range attempts {
		if now.Sub(t) < loginWindow {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= loginMaxAttempts {
		s.loginAttempts[ip] = fresh
		s.loginMu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"detail": "Too many login attempts. Try again later."})
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	if !VerifyAccessToken(strings.TrimSpace(body.Token)) {
		fresh = append(fresh, now)
		s.loginAttempts[ip] = fresh
		s.loginMu.Unlock()
		writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Invalid access token."})
		return
	}
	delete(s.loginAttempts, ip)
	s.loginMu.Unlock()

	secret, _ := config.LoadConfig()[authSecretKey].(string)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    CreateSessionCookie(secret),
		Path:     "/",
		MaxAge:   SessionTTL,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.UseSSL(),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// --- Info / config ---------------------------------------------------------

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	cfg := config.LoadConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"local_ip":        localIP(),
		"port":            s.port,
		"active_provider": sanitizeProvider(cfg["active_provider"]),
		"config_path":     config.ConfigPath,
	})
}

func (s *Server) handleLanguages(w http.ResponseWriter, _ *http.Request) {
	langs := map[string]string{}
	for _, l := range config.LanguageChoices {
		langs[l.Code] = l.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{"languages": langs})
}

func (s *Server) handleCurrencies(w http.ResponseWriter, _ *http.Request) {
	if data, err := httpGetJSON("https://open.er-api.com/v6/latest/USD", nil, 5*time.Second); err == nil {
		if rates, ok := data["rates"].(map[string]any); ok && len(rates) > 0 {
			list := make([]string, 0, len(rates))
			for k := range rates {
				list = append(list, k)
			}
			sort.Strings(list)
			writeJSON(w, http.StatusOK, map[string]any{"currencies": list, "rates": rates})
			return
		}
	}
	fallback := []string{"USD", "EUR", "GBP", "ARS", "BRL", "MXN", "CAD", "AUD", "JPY", "CNY", "CLP", "COP", "PEN"}
	writeJSON(w, http.StatusOK, map[string]any{"currencies": fallback, "rates": map[string]any{"USD": 1.0}})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := config.LoadConfig()
	masked := map[string]any{}
	for k, v := range cfg {
		if isAuthKey(k) {
			continue
		}
		masked[k] = v
	}
	masked["active_provider"] = sanitizeProvider(masked["active_provider"])
	for k := range masked {
		if strings.HasSuffix(k, "_api_key") {
			if s, _ := masked[k].(string); s != "" {
				masked[k] = "********"
			} else {
				masked[k] = ""
			}
		}
	}
	writeJSON(w, http.StatusOK, masked)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, req *http.Request) {
	var data map[string]any
	if err := json.NewDecoder(req.Body).Decode(&data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid JSON body."})
		return
	}
	cfg := config.LoadConfig()
	for k, v := range data {
		if isAuthKey(k) {
			continue
		}
		if strings.HasSuffix(k, "_api_key") {
			list, _ := v.(string)
			if strings.TrimSpace(list) == "" || strings.Contains(list, "********") || strings.Contains(list, "...") {
				continue
			}
		}
		if k == "active_provider" {
			v = sanitizeProvider(v)
		}
		cfg[k] = v
	}
	if err := config.SaveConfig(cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to save configuration file."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "Configuration saved successfully."})
}

// --- Models ----------------------------------------------------------------

func (s *Server) handleModels(w http.ResponseWriter, req *http.Request) {
	provider := sanitizeProvider(req.URL.Query().Get("provider"))
	cfg := config.LoadConfig()
	apiKey := config.GetAPIKey(provider, cfg)

	type pricing struct {
		Prompt         float64 `json:"prompt"`
		PromptCost     float64 `json:"prompt_cost"`
		Completion     float64 `json:"completion"`
		CompletionCost float64 `json:"completion_cost"`
		ContextLength  int     `json:"context_length"`
		Reasoning      bool    `json:"reasoning"`
	}
	models := []string{}
	pricingMap := map[string]pricing{}

	switch provider {
	case "OpenRouter":
		if data, err := s.openRouterModels(apiKey); err == nil {
			for _, item := range data {
				id, _ := item["id"].(string)
				if id == "" {
					continue
				}
				pr, _ := item["pricing"].(map[string]any)
				p := asFloat(pr["prompt"])
				c := asFloat(pr["completion"])
				ctx := asInt(item["context_length"])
				if ctx == 0 {
					ctx = 128000
				}
				models = append(models, id)
				pricingMap[id] = pricing{p, p, c, c, ctx, supportsReasoning(id) || hasReasoningParam(item)}
			}
		}
		if len(models) == 0 {
			for _, id := range config.OpenRouterModelFallback {
				kc := config.KnownCosts[id]
				models = append(models, id)
				pricingMap[id] = pricing{kc[0], kc[0], kc[1], kc[1], 128000, supportsReasoning(id)}
			}
		}
	case "OpenCode Zen", "OpenCode Go":
		models = s.openCodeModels(apiKey, provider == "OpenCode Zen")
		for _, id := range models {
			pricingMap[id] = pricing{0, 0, 0, 0, 128000, supportsReasoning(id)}
		}
	default:
		models = config.ProviderModels[provider]
		for _, id := range models {
			kc := config.KnownCosts[id]
			pricingMap[id] = pricing{kc[0], kc[0], kc[1], kc[1], 128000, supportsReasoning(id)}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider": provider, "models": models, "pricing": pricingMap,
	})
}

func (s *Server) openRouterModels(apiKey string) ([]map[string]any, error) {
	s.modelsMu.Lock()
	if s.modelsCache != nil && time.Since(s.modelsTime) < 5*time.Minute {
		cached := s.modelsCache
		s.modelsMu.Unlock()
		return cached, nil
	}
	s.modelsMu.Unlock()

	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	data, err := httpGetJSON("https://openrouter.ai/api/v1/models", headers, 5*time.Second)
	if err != nil {
		return nil, err
	}
	raw, _ := data["data"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	s.modelsMu.Lock()
	s.modelsCache = out
	s.modelsTime = time.Now()
	s.modelsMu.Unlock()
	return out, nil
}

func (s *Server) openCodeModels(apiKey string, zen bool) []string {
	base := "https://opencode.ai/zen/go/v1"
	if zen {
		base = "https://opencode.ai/zen/v1"
	}
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	if data, err := httpGetJSON(base+"/models", headers, 5*time.Second); err == nil {
		if raw, ok := data["data"].([]any); ok {
			var out []string
			for _, item := range raw {
				if m, ok := item.(map[string]any); ok {
					if id, ok := m["id"].(string); ok && id != "" {
						out = append(out, id)
					}
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return config.OpenCodeZenModels
}

func (s *Server) handleModelEndpoints(w http.ResponseWriter, req *http.Request) {
	modelID := req.URL.Query().Get("model_id")
	if modelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Missing model_id parameter."})
		return
	}
	data, err := httpGetJSON("https://openrouter.ai/api/v1/models/"+modelID+"/endpoints", nil, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "data": map[string]any{"endpoints": []any{}}})
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) getModelPricing(modelID string) (float64, float64, int) {
	kc, ok := config.KnownCosts[modelID]
	p, c := 0.0, 0.0
	if ok {
		p, c = kc[0], kc[1]
	}
	ctx := 128000
	if (p == 0 && c == 0) || strings.HasPrefix(modelID, "openrouter/") || strings.Contains(modelID, "/") {
		if data, err := s.openRouterModels(""); err == nil {
			for _, item := range data {
				if id, _ := item["id"].(string); id == modelID {
					pr, _ := item["pricing"].(map[string]any)
					p = asFloat(pr["prompt"])
					c = asFloat(pr["completion"])
					if v := asInt(item["context_length"]); v > 0 {
						ctx = v
					}
					break
				}
			}
		}
	}
	return p, c, ctx
}

// --- Upload / download -----------------------------------------------------

var allowedExts = map[string]struct{}{".srt": {}, ".ass": {}, ".vtt": {}}

func (s *Server) handleUpload(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid multipart form."})
		return
	}
	files := req.MultipartForm.File["files"]
	if len(files) > maxUploadFiles {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("Too many files. Maximum is %d per upload.", maxUploadFiles)})
		return
	}

	type uploaded struct {
		Filename     string `json:"filename"`
		OriginalName string `json:"original_name"`
		Filepath     string `json:"filepath"`
		Size         int64  `json:"size"`
	}
	var result []uploaded

	for _, fh := range files {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if _, ok := allowedExts[ext]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("Unsupported file format '%s'. Only .srt, .ass, and .vtt files are allowed.", ext)})
			return
		}
		safe := filepath.Base(fh.Filename)
		targetName := uuid8() + "_" + safe
		targetPath := filepath.Join(s.uploadDir, targetName)

		src, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to read upload."})
			return
		}
		dst, err := os.Create(targetPath)
		if err != nil {
			src.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to store upload."})
			return
		}
		written, err := io.Copy(dst, io.LimitReader(src, maxUploadBytes+1))
		src.Close()
		dst.Close()
		if err != nil {
			os.Remove(targetPath)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to store upload."})
			return
		}
		if written > maxUploadBytes {
			os.Remove(targetPath)
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"detail": fmt.Sprintf("'%s' exceeds the %d MB limit.", safe, maxUploadBytes/(1024*1024))})
			return
		}

		result = append(result, uploaded{targetName, safe, targetPath, written})
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded_files": result})
}

func (s *Server) handleDownload(w http.ResponseWriter, req *http.Request) {
	safe := filepath.Base(chi.URLParam(req, "filename"))
	if safe == "" || safe == "." || safe == ".." {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid filename."})
		return
	}

	dirs := []string{s.outputDir, s.uploadDir, s.baseDir}
	for _, d := range dirs {
		target := filepath.Join(d, safe)
		if isWithin(d, target) {
			if info, err := os.Stat(target); err == nil && !info.IsDir() {
				serveDownload(w, req, target, userFacingName(safe))
				return
			}
		}
	}

	suffix := "_" + safe
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
				continue
			}
			serveDownload(w, req, filepath.Join(d, e.Name()), userFacingName(e.Name()))
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"detail": fmt.Sprintf("File '%s' not found.", safe)})
}

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

	fmt.Fprint(w, "data: [Connected to LLM Translator Log Stream]\n\n")
	flusher.Flush()

	for {
		select {
		case <-req.Context().Done():
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
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid JSON body."})
		return
	}
	session, filePaths, excluded, err := s.buildSession(data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}

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
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
		return
	}
	// Only the actual translation run pays for the glossary enrichment call.
	session.EnrichGlossary = true

	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	s.translationActive = true
	s.translationMu.Unlock()

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
			s.broker.Broadcast("\n=== TRANSLATION CANCELLED ===\n")
		} else {
			s.broker.Broadcast("\n=== TRANSLATION COMPLETED ===\n")
		}
	}()

	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "file_count": len(filePaths)})
}

func (s *Server) handleCancel(w http.ResponseWriter, _ *http.Request) {
	s.translationMu.Lock()
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	s.translationMu.Unlock()
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
		ctxLen = 128000
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

// --- Static ----------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	htmlPath := filepath.Join(s.staticDir, "index.html")
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<h2>LLM Subtitle Translator Web Server</h2><p>Static index.html not found.</p>")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) handleFavicon(w http.ResponseWriter, _ *http.Request) {
	fav := filepath.Join(s.staticDir, "favicon.ico")
	if _, err := os.Stat(fav); err != nil {
		http.NotFound(w, nil)
		return
	}
	http.ServeFile(w, nil, fav)
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func sanitizeProvider(v any) string {
	name, _ := v.(string)
	for _, p := range config.ValidProviders {
		if p == name {
			return name
		}
	}
	return "OpenRouter"
}

// reasoningMarkers identifies model families that support an effort/reasoning knob.
var reasoningMarkers = []string{
	"o1", "o3", "o4", "gpt-5", "deepseek-r1", "deepseek-reasoner", "deepseek-v4",
	"qwq", "qvq", "-think", "glm-4.6", "glm-5", "kimi-k2", "minimax-m",
	"claude-3-7", "claude-sonnet-4", "claude-opus-4", "gemini-2.5", "gemini-3",
}

func supportsReasoning(modelID string) bool {
	m := strings.ToLower(modelID)
	for _, k := range reasoningMarkers {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

func hasReasoningParam(item map[string]any) bool {
	params, ok := item["supported_parameters"].([]any)
	if !ok {
		return false
	}
	for _, p := range params {
		if s, ok := p.(string); ok {
			switch s {
			case "reasoning", "include_reasoning", "reasoning_effort":
				return true
			}
		}
	}
	return false
}

func isAuthKey(k string) bool {
	for _, a := range AuthKeys {
		if k == a {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func localIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// LocalIP exposes the detected LAN IP for the CLI entrypoint.
func LocalIP() string { return localIP() }

func uuid8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func strDefault(data map[string]any, key, def string) string {
	if v, ok := data[key].(string); ok && v != "" {
		return v
	}
	return def
}

func isWithin(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

var uuidPrefix = regexp.MustCompile(`^[0-9a-fA-F]{8}_`)

func userFacingName(name string) string {
	return uuidPrefix.ReplaceAllString(name, "")
}

func serveDownload(w http.ResponseWriter, req *http.Request, path, filename string) {
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, req, path)
}

func httpGetJSON(url string, headers map[string]string, timeout time.Duration) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("HTTP-Referer", "https://pepin208.dedyn.io")
	req.Header.Set("X-Title", "LLMT")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
