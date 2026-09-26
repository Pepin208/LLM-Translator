package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"translate_llm/internal/config"
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
		fresh = append(fresh, now)
		s.loginAttempts[ip] = fresh
		s.loginMu.Unlock()
		// Surface the current token in the terminal so the user can type it on
		// the device that is trying to log in.
		s.logger.Warn("login failed: invalid access token",
			"ip", ip, "ua", ua, "access_token", CurrentAccessToken())
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
