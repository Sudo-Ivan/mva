package config

import (
	"net/http/httptest"
	"testing"
)

func TestRequestPublicURLConfigured(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost:8077/", nil)
	got := RequestPublicURL(r, "https://mc.example.com", false)
	if got != "https://mc.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestRequestPublicURLTrustProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1:8077/", nil)
	r.Host = "127.0.0.1:8077"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "mc.example.com")

	got := RequestPublicURL(r, "http://localhost:8080", true)
	if got != "https://mc.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestRequestPublicURLTrustProxyUsesHost(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost:8077/", nil)
	r.Host = "localhost:8077"

	got := RequestPublicURL(r, "http://localhost:8080", true)
	if got != "http://localhost:8077" {
		t.Fatalf("got %q", got)
	}
}

func TestRequestPublicURLRejectsInjectedHeaders(t *testing.T) {
	cases := []struct {
		name  string
		proto string
		host  string
		want  string
	}{
		{
			name:  "bad proto falls back to request scheme",
			proto: "javascript",
			host:  "mc.example.com",
			want:  "http://mc.example.com",
		},
		{
			name:  "proto with payload is ignored",
			proto: "https://evil.example.com",
			host:  "mc.example.com",
			want:  "http://mc.example.com",
		},
		{
			name: "host with path is rejected",
			host: "mc.example.com/evil",
			want: "http://origin.example.com",
		},
		{
			name: "host with userinfo is rejected",
			host: "user@evil.example.com",
			want: "http://origin.example.com",
		},
		{
			name: "host with control characters is rejected",
			host: "evil.example.com\r\nX-Injected: 1",
			want: "http://origin.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://origin.example.com/", nil)
			r.Host = "origin.example.com"
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.host != "" {
				r.Header.Set("X-Forwarded-Host", tc.host)
			}
			if got := RequestPublicURL(r, "http://configured.example.com", true); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestPublicURLInvalidEverythingFallsBack(t *testing.T) {
	r := httptest.NewRequest("GET", "http://origin.example.com/", nil)
	r.Host = "bad host"
	r.Header.Set("X-Forwarded-Host", "bad/host")
	r.Header.Set("X-Forwarded-Proto", "ftp")
	if got := RequestPublicURL(r, "https://configured.example.com", true); got != "https://configured.example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyArchiveOverridesPreservesExplicitPublicURL(t *testing.T) {
	cfg := &Config{
		Listen:     ":8080",
		PublicURL:  "https://mc.example.com",
		CacheBytes: 1,
	}
	if err := cfg.ApplyServeOverrides("", 8077, "", ""); err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8077" {
		t.Fatalf("Listen = %q", cfg.Listen)
	}
	if cfg.PublicURL != "https://mc.example.com" {
		t.Fatalf("PublicURL = %q", cfg.PublicURL)
	}
}

func TestApplyServeOverridesPublicURLFlag(t *testing.T) {
	cfg := &Config{Listen: ":8080", PublicURL: "http://localhost:8080", CacheBytes: 1}
	if err := cfg.ApplyServeOverrides("", 0, "", "https://mirror.example.com"); err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://mirror.example.com" {
		t.Fatalf("PublicURL = %q", cfg.PublicURL)
	}
}
