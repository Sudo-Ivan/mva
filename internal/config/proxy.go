package config

import (
	"net/http"
	"strings"
)

func RequestPublicURL(r *http.Request, configured string, trustProxy bool) string {
	configured = strings.TrimRight(configured, "/")
	if !trustProxy {
		return configured
	}
	host := forwardedHost(r)
	if host == "" {
		return configured
	}
	return forwardedProto(r) + "://" + host
}

// forwardedProto returns the scheme a trusted proxy reports. Anything other
// than http or https is ignored, since the value ends up inside rewritten
// JSON download URLs.
func forwardedProto(r *http.Request) string {
	if p := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); p == "http" || p == "https" {
		return p
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// forwardedHost prefers X-Forwarded-Host and falls back to the request Host.
// Values that cannot be a bare host[:port] are dropped so header injection
// cannot reshape the public URL.
func forwardedHost(r *http.Request) string {
	if h := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); validHost(h) {
		return h
	}
	if h := strings.TrimSpace(r.Host); validHost(h) {
		return h
	}
	return ""
}

func validHost(h string) bool {
	if h == "" || len(h) > 255 {
		return false
	}
	for i := 0; i < len(h); i++ {
		switch c := h[i]; {
		case c <= ' ' || c == 0x7f:
			return false
		case c == '/' || c == '\\' || c == '@' || c == '#' || c == '?' || c == '"' || c == '\'':
			return false
		}
	}
	return true
}
