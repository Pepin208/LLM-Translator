package web

import (
	"io"
	"log/slog"
	"os"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

// NewLogger builds the server logger. Output is verbose (debug level) and goes
// both to stdout (so it is visible in the terminal running the server) and to
// the shared translator_log.txt, best-effort.
func NewLogger() *slog.Logger {
	writers := []io.Writer{os.Stdout}
	if f, err := os.OpenFile(config.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		writers = append(writers, f)
	}
	return slog.New(slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}
