package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

const (
	loginMaxAttempts = 5
	loginWindow      = 60 * time.Second
)

func (s *Server) handleLogin(w http.ResponseWriter, req *http.Request) {
	ip := clientIP(req)
	ua := req.UserAgent()
	s.logger.Info("login requested", "ip", ip, "ua", ua)

	now := time.Now()

	s.loginMu.Lock()
	// Opportunistically drop IPs whose attempts have all expired, so the map
	// does not grow without bound on a public-facing instance.
	s.pruneLoginAttemptsLocked(now)
	if len(s.loginAttempts[ip]) >= loginMaxAttempts {
		s.loginMu.Unlock()
		s.logger.Warn("login rate-limited", "ip", ip, "ua", ua)
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"detail": "Too many login attempts. Try again later."})
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	submitted := strings.TrimSpace(body.Token)

	if !VerifyAccessToken(submitted) {
		s.loginAttempts[ip] = append(s.loginAttempts[ip], now)
		s.loginMu.Unlock()
		// Audit log for the failed attempt. Deliberately no secret material:
		// this logger writes to stdout and translator_log.txt.
		s.logger.Warn("login failed: invalid access token", "ip", ip, "ua", ua)
		// Preserve the lockout UX by showing the current token on the server's
		// own terminal only. It must NOT go through s.logger or the SSE broker.
		if token := CurrentAccessToken(); token != "" {
			fmt.Fprintf(os.Stderr, "\n[login] device %s failed to authenticate; current access token: %s\n", ip, token)
		}
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
	s.logger.Info("login succeeded", "ip", ip, "ua", ua)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleLogout(w http.ResponseWriter, req *http.Request) {
	s.logger.Info("logout", "ip", clientIP(req), "ua", req.UserAgent())
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// pruneLoginAttemptsLocked drops every IP whose stored attempts are all outside
// the rate-limit window. Callers must hold s.loginMu.
func (s *Server) pruneLoginAttemptsLocked(now time.Time) {
	for ip, attempts := range s.loginAttempts {
		fresh := attempts[:0]
		for _, t := range attempts {
			if now.Sub(t) < loginWindow {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) == 0 {
			delete(s.loginAttempts, ip)
			continue
		}
		s.loginAttempts[ip] = fresh
	}
}
