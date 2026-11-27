package fetch

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestSharedTransportReuse(t *testing.T) {
	a := sharedHTTPClient(time.Minute)
	b := sharedHTTPClient(time.Minute)
	if a.Transport != b.Transport {
		t.Fatal("expected shared transport instance")
	}
}

func TestSharedTransportPoolingDefaults(t *testing.T) {
	tr := sharedTransport()
	if tr.MaxIdleConns != maxIdleConns {
		t.Fatalf("MaxIdleConns = %d", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != maxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d", tr.MaxIdleConnsPerHost)
	}
}

func TestUpstreamRedirectPolicy(t *testing.T) {
	mkreq := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: u}
	}
	for _, raw := range []string{
		"https://piston-data.mojang.com/file",
		"https://libraries.minecraft.net/org/example.jar",
		"https://release-assets.githubusercontent.com/asset",
	} {
		if err := upstreamRedirectPolicy(mkreq(raw), nil); err != nil {
			t.Errorf("%s rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://libraries.minecraft.net/org/example.jar",
		"https://169.254.169.254/latest/meta-data",
		"https://evil.example.com/file",
	} {
		if err := upstreamRedirectPolicy(mkreq(raw), nil); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
}
