package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Pepin208/LLM-Translator/internal/config"
)

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
	if data, err := httpGetJSON(config.CurrencyRatesURL, nil, 5*time.Second); err == nil {
		if rates, ok := data["rates"].(map[string]any); ok && len(rates) > 0 {
			list := make([]string, 0, len(rates))
			for k := range rates {
				list = append(list, k)
			}
			sort.Strings(list)
			writeJSON(w, http.StatusOK, map[string]any{"currencies": list, "rates": rates})
			return
		}
	} else {
		s.logger.Warn("currency rates fetch failed; using fallback", "error", err)
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
		s.logger.Warn("config update rejected: invalid JSON", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid JSON body."})
		return
	}
	cfg := config.LoadConfig()
	var changed []string
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
		changed = append(changed, k)
	}
	if err := config.SaveConfig(cfg); err != nil {
		s.logger.Error("config save failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to save configuration file."})
		return
	}
	sort.Strings(changed)
	s.logger.Info("config updated", "keys", changed, "ip", clientIP(req))
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "Configuration saved successfully."})
}
