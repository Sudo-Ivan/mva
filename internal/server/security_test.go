package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"minecraft-version-archive/internal/archive"
	"minecraft-version-archive/internal/config"
	"minecraft-version-archive/internal/stats"
)

func TestTraversalRoutesRejected(t *testing.T) {
	ts, _ := testServer(t, false)
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	urls := []string{
		"/versions/..%2F..%2Fetc%2Fpasswd",
		"/versions/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/versions/..%5C..%5Cetc%5Cpasswd",
		"/jars/client/..%2F..%2Fetc%2Fpasswd",
		"/jars/server/..%2F..%2Fsettings.ini",
		"/libraries/..%2F..%2Fsettings.ini",
		"/libraries/%2e%2e/%2e%2e/settings.ini",
		"/assets/indexes/..%2F..%2Fsettings.ini",
		"/assets/objects/..%2F..%2Fetc%2F0123456789abcdef0123456789abcdef01234567",
		"/fabric/v2/..%2F..%2Fsettings.ini",
		"/maven/fabricmc/..%2F..%2Fsettings.ini",
		"/meta/v1/..%2F..%2Fsettings.ini",
	}

	for _, u := range urls {
		resp, err := client.Get(ts.URL + u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%s: status 200", u)
		}
		if strings.Contains(string(body), "root:") || strings.Contains(string(body), "[general]") {
			t.Errorf("%s: served file outside the data dir", u)
		}
	}
}

func TestHTMLPagesCarrySecurityHeaders(t *testing.T) {
	ts, _ := testServer(t, false)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP %q missing %q", csp, directive)
		}
	}
}

func TestStartAutoRefreshRejectsNonPositiveInterval(t *testing.T) {
	for _, min := range []int{0, -5} {
		cfg := &config.Config{DataDir: t.TempDir(), RefreshMin: min, CacheBytes: 1}
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		srv := New(cfg, archive.NewWithStats(cfg, log, stats.New()), log)
		srv.StartAutoRefresh(context.Background())
	}
}
