package web

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Pepin208/LLM-Translator/internal/config"
	"github.com/Pepin208/LLM-Translator/internal/webui"
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
	baseDir      string
	staticFS     fs.FS
	staticSource string
	uploadDir    string
	outputDir    string
	port         int
	useTLS       bool

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
	staticFS, staticSource := resolveStaticFS()
	s := &Server{
		baseDir:       baseDir,
		staticFS:      staticFS,
		staticSource:  staticSource,
		uploadDir:     uploadDir,
		outputDir:     outputDir,
		port:          config.ResolvePort(),
		useTLS:        certsExist(baseDir),
		broker:        NewBroker(),
		loginAttempts: map[string][]time.Time{},
		logger:        NewLogger(),
	}
	s.logger.Info("server initialised",
		"base_dir", baseDir,
		"static_source", staticSource,
		"upload_dir", uploadDir,
		"output_dir", outputDir,
		"port", s.port,
	)
	return s
}

// resolveStaticFS prefers a real directory when $TRANSLATOR_STATIC points at
// one (development: edit the frontend without recompiling), and falls back to
// the embedded frontend so release binaries are self-contained.
func resolveStaticFS() (fs.FS, string) {
	if dir := strings.TrimSpace(os.Getenv("TRANSLATOR_STATIC")); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return os.DirFS(dir), "disk:" + dir
		}
	}
	return webui.FS(), "embedded"
}

// certsExist reports whether cert.pem/key.pem are present in baseDir.
func certsExist(baseDir string) bool {
	_, e1 := os.Stat(filepath.Join(baseDir, "cert.pem"))
	_, e2 := os.Stat(filepath.Join(baseDir, "key.pem"))
	return e1 == nil && e2 == nil
}

// UseSSL reports whether the server should serve TLS (Secure cookies).
func (s *Server) UseSSL() bool { return s.useTLS }

// SetTLS overrides the TLS decision (e.g. --http forces it off).
func (s *Server) SetTLS(on bool) { s.useTLS = on }

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
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(s.staticFS))))
	r.Get("/", s.handleIndex)
	return r
}

// --- Static ----------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(s.staticFS, "index.html")
	if err != nil {
		s.logger.Error("index.html not found", "source", s.staticSource, "error", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<h2>LLM Subtitle Translator Web Server</h2><p>Static index.html not found.</p>")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) handleFavicon(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(s.staticFS, "favicon.ico")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "image/x-icon")
	w.Write(data)
}
