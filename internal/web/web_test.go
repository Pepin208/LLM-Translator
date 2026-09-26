package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"translate_llm/internal/config"
)

func withTempConfig(t *testing.T) {
	t.Helper()
	old := config.ConfigPath
	config.ConfigPath = filepath.Join(t.TempDir(), "translator_config.json")
	_ = config.SaveConfig(map[string]any{"openrouter_api_key": "sk-secret-123456"})
	t.Cleanup(func() { config.ConfigPath = old })
}

func TestEnsureAuthConfigGeneratesOnce(t *testing.T) {
	withTempConfig(t)
	token, generated := EnsureAuthConfig()
	if !generated || token == "" {
		t.Fatalf("expected a generated token")
	}
	_, again := EnsureAuthConfig()
	if again {
		t.Errorf("token must only be generated once")
	}
	if !VerifyAccessToken(token) {
		t.Errorf("generated token should verify")
	}
	if VerifyAccessToken("nope") {
		t.Errorf("wrong token must not verify")
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	secret := "secret"
	c := CreateSessionCookie(secret)
	if !verifySessionCookie(secret, c) {
		t.Fatalf("valid cookie rejected")
	}
	if verifySessionCookie(secret, c+"x") {
		t.Errorf("tampered cookie accepted")
	}
	if verifySessionCookie("other", c) {
		t.Errorf("cookie verified with wrong secret")
	}
	if verifySessionCookie(secret, "not-a-cookie") {
		t.Errorf("malformed cookie accepted")
	}
}

func TestModelsIncludeReasoningFlag(t *testing.T) {
	withTempConfig(t)
	token, _ := EnsureAuthConfig()

	srv := NewServer(t.TempDir())
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = client.Get(ts.URL + "/api/models?provider=OpenAI")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		Pricing map[string]struct {
			Reasoning bool `json:"reasoning"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload.Pricing["gpt-4o-mini"]; !ok {
		t.Fatalf("missing pricing entry: %v", payload.Pricing)
	}
	if payload.Pricing["o1"].Reasoning != true {
		t.Errorf("o1 should be flagged as reasoning-capable")
	}
	if payload.Pricing["gpt-4o"].Reasoning {
		t.Errorf("gpt-4o should not be flagged as reasoning-capable")
	}
}

func TestAuthEnforcedAndConfigMasked(t *testing.T) {
	withTempConfig(t)
	token, _ := EnsureAuthConfig()

	srv := NewServer(t.TempDir())
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Unauthenticated request is rejected.
	resp, err := client.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Login.
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err = client.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Authenticated config is masked and never leaks auth keys.
	resp, err = client.Get(ts.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authed config status = %d", resp.StatusCode)
	}
	var cfg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	if cfg["openrouter_api_key"] != "********" {
		t.Errorf("key not masked: %v", cfg["openrouter_api_key"])
	}
	for _, k := range AuthKeys {
		if _, ok := cfg[k]; ok {
			t.Errorf("auth key %q leaked", k)
		}
	}

	// Logout then rejected again.
	resp, _ = client.Post(ts.URL+"/api/logout", "application/json", nil)
	resp.Body.Close()
	resp, _ = client.Get(ts.URL + "/api/config")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout status = %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}
