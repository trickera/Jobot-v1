package server

import (
	"strings"
)

func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "nao configurada"
	}
	if len(key) <= 4 {
		// Too short to reveal any tail without leaking the whole key (and a
		// naive key[len-4:] would panic for len < 4).
		return "****"
	}
	if len(key) <= 8 {
		return "..." + key[len(key)-4:]
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func geminiModelForConfig(config appConfig) string {
	if model := strings.TrimSpace(config.Form.Model); model != "" {
		return model
	}
	return geminiFreeModel
}

func isRateLimitStatus(code int) bool {
	return code == 429 || code == 503
}
