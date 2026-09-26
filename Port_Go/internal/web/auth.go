// Package web implements the LAN HTTP server: token authentication, SSE log
// streaming, upload/download and the translation control endpoints.
package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"translate_llm/internal/config"
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.TrimRight(hex.EncodeToString(b), "=")
}

const (
	SessionCookie    = "llmt_session"
	SessionTTL       = 7 * 24 * 3600
	authSecretKey    = "auth_secret"
	authTokenHashKey = "auth_token_hash"
	// authTokenKey stores the plaintext access token so the server can re-show
	// it in the terminal when a new device asks to log in. The config file is
	// gitignored and already holds API keys in plaintext.
	authTokenKey = "auth_token"
)

// AuthKeys are the config keys that must never be exposed or overwritten.
var AuthKeys = []string{authSecretKey, authTokenHashKey, authTokenKey}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EnsureAuthConfig creates the cookie secret, access token and token hash when
// missing. It always returns the current token; generated reports whether this
// call created a new one (which invalidates previously issued tokens).
func EnsureAuthConfig() (token string, generated bool) {
	cfg := config.LoadConfig()
	changed := false

	if s, _ := cfg[authSecretKey].(string); s == "" {
		cfg[authSecretKey] = randomHex(32)
		changed = true
	}

	token, _ = cfg[authTokenKey].(string)
	if token == "" {
		token = randomToken(32)
		cfg[authTokenKey] = token
		cfg[authTokenHashKey] = hashToken(token)
		changed = true
		generated = true
	} else if h, _ := cfg[authTokenHashKey].(string); h == "" {
		// Backfill the hash for configs created before the plaintext was kept.
		cfg[authTokenHashKey] = hashToken(token)
		changed = true
	}

	if changed {
		_ = config.SaveConfig(cfg)
	}
	return token, generated
}

// CurrentAccessToken returns the stored plaintext access token, or "" if none.
func CurrentAccessToken() string {
	t, _ := config.LoadConfig()[authTokenKey].(string)
	return t
}

// RotateAccessToken generates and persists a new access token.
func RotateAccessToken() string {
	cfg := config.LoadConfig()
	token := randomToken(32)
	cfg[authSecretKey] = randomHex(32)
	cfg[authTokenKey] = token
	cfg[authTokenHashKey] = hashToken(token)
	_ = config.SaveConfig(cfg)
	return token
}

// VerifyAccessToken compares a submitted token against the stored hash.
func VerifyAccessToken(token string) bool {
	if token == "" {
		return false
	}
	stored, _ := config.LoadConfig()[authTokenHashKey].(string)
	if stored == "" {
		return false
	}
	return hmac.Equal([]byte(stored), []byte(hashToken(token)))
}

// CreateSessionCookie builds a signed, timestamped cookie value.
func CreateSessionCookie(secret string) string {
	issued := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(issued))
	return issued + "." + hex.EncodeToString(mac.Sum(nil))
}

func verifySessionCookie(secret, value string) bool {
	issued, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(issued))
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return false
	}
	ts, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return false
	}
	age := time.Now().Unix() - ts
	return age >= 0 && age <= SessionTTL
}

// IsAuthenticated reports whether the request carries a valid session cookie.
func IsAuthenticated(r *http.Request) bool {
	secret, _ := config.LoadConfig()[authSecretKey].(string)
	if secret == "" {
		return false
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return false
	}
	return verifySessionCookie(secret, c.Value)
}
