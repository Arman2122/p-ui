package mtproto

import (
	"encoding/json"
	"strings"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// defaultFakeTLSDomain fronts a generated secret when the inbound names no
// domain of its own; it mirrors the frontend's default.
const defaultFakeTLSDomain = "www.cloudflare.com"

// FakeTLSDomainFromSettings returns the inbound-level fronting domain, falling
// back to the default, so a generated client secret always fronts a real host.
func FakeTLSDomainFromSettings(settings string) string {
	domain := ""
	if settings != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(settings), &m); err == nil {
			domain, _ = m["fakeTlsDomain"].(string)
		}
	}
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return defaultFakeTLSDomain
	}
	return domain
}

// MintFakeTLSSecret builds a client secret fronting the settings' domain.
func MintFakeTLSSecret(settings string) string {
	return model.GenerateFakeTLSSecret(FakeTLSDomainFromSettings(settings))
}

// ValidAdTag reports whether a Telegram advertising tag is well-formed.
func ValidAdTag(tag string) bool { return model.ValidMtprotoAdTag(tag) }
