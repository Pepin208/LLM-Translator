package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"translate_llm/internal/config"
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
	logger *slog.Logger

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
	s := &Server{
		baseDir:       baseDir,
		staticDir:     filepath.Join(baseDir, "static"),
		uploadDir:     uploadDir,
		outputDir:     outputDir,
		port:          config.DefaultPort,
		broker:        NewBroker(),
		loginAttempts: map[string][]time.Time{},
		logger:        NewLogger(),
	}
	s.logger.Info("server initialised",
		"base_dir", baseDir,
		"static_dir", s.staticDir,
		"upload_dir", uploadDir,
		"output_dir", outputDir,
		"port", s.port,
	)
	return s
}

// UseSSL reports whether cert.pem/key.pem exist in the project root.
func (s *Server) UseSSL() bool {
	_, e1 := os.Stat(filepath.Join(s.baseDir, "cert.pem"))
	_, e2 := os.Stat(filepath.Join(s.baseDir, "key.pem"))
	return e1 == nil && e2 == nil
}

// Logger exposes the server logger.
func (s *Server) Logger() *slog.Logger { return s.logger }

// Router builds the HTTP handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.loggingMiddleware)
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

// --- Static ----------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	htmlPath := filepath.Join(s.staticDir, "index.html")
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		s.logger.Error("index.html not found", "path", htmlPath, "error", err)
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
