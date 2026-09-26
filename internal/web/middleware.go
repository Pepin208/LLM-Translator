package web

import (
	"net/http"
	"strings"
	"time"
)

// statusRecorder captures the status code and byte count for request logging.
// It forwards Flush so SSE streaming keeps working through the wrapper.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// loggingMiddleware emits one structured line per request with method, path,
// status, size, latency, client IP and user agent.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, req)
		s.logger.Info("http",
			"method", req.Method,
			"path", req.URL.Path,
			"query", req.URL.RawQuery,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(req),
			"ua", req.UserAgent(),
		)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if strings.HasPrefix(path, "/api/") && path != "/api/login" {
			if !IsAuthenticated(req) {
				s.logger.Warn("unauthenticated request blocked",
					"path", path, "ip", clientIP(req), "ua", req.UserAgent())
				writeJSON(w, http.StatusUnauthorized, map[string]any{"detail": "Authentication required."})
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}
