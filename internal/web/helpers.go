package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

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

func httpGetJSON(url string, headers map[string]string, timeout time.Duration) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("HTTP-Referer", config.RefererURL())
	req.Header.Set("X-Title", config.AppTitle)
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
