package fetch

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxIdleConns        = 256
	maxIdleConnsPerHost = 64
	idleConnTimeout     = 90 * time.Second
	dialTimeout         = 30 * time.Second
	keepAlive           = 30 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
	responseHdrTimeout  = 30 * time.Second
)

var sharedTransport = sync.OnceValue(newTransport)

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHdrTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

var upstreamRedirectHosts = map[string]bool{
	"piston-meta.mojang.com":               true,
	"piston-data.mojang.com":               true,
	"libraries.minecraft.net":              true,
	"resources.download.minecraft.net":     true,
	"meta.fabricmc.net":                    true,
	"maven.fabricmc.net":                   true,
	"api.adoptium.net":                     true,
	"github.com":                           true,
	"release-assets.githubusercontent.com": true,
	"objects.githubusercontent.com":        true,
}

func upstreamRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to non-https URL %q rejected", req.URL.Redacted())
	}
	if !upstreamRedirectHosts[strings.ToLower(req.URL.Hostname())] {
		return fmt.Errorf("redirect to unexpected host %q rejected", req.URL.Hostname())
	}
	return nil
}

func sharedHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     sharedTransport(),
		Timeout:       timeout,
		CheckRedirect: upstreamRedirectPolicy,
	}
}

func SharedPeerHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: sharedTransport(),
		Timeout:   timeout,
	}
}
