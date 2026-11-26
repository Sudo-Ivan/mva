package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"minecraft-version-archive/internal/coalesce"
	"minecraft-version-archive/internal/constants"
	"minecraft-version-archive/internal/fabric"
	"minecraft-version-archive/internal/fetch"
	"minecraft-version-archive/internal/integrity"
	"minecraft-version-archive/internal/mojang"
	"minecraft-version-archive/internal/safepath"
	"minecraft-version-archive/internal/store"
)

const manifestCacheTTL = 5 * time.Minute

type manifestCache struct {
	manifest *mojang.Manifest
	expiry   time.Time
}

type proxyHelper struct {
	client     *fetch.Client
	mu         sync.Mutex
	cached     *manifestCache
	layout     *store.Layout
	proxyCache bool
	hasDataDir bool
	group      *coalesce.Loader
	log        *slog.Logger
}

func newProxyHelper(layout *store.Layout, retryMax, retryMS int, proxyCache bool, log *slog.Logger) *proxyHelper {
	client := fetch.New(retryMax, retryMS)
	hasDataDir := false
	if d, err := layout.FS(); err == nil {
		client = client.WithDataDir(d)
		hasDataDir = true
	}
	return &proxyHelper{
		client:     client,
		layout:     layout,
		proxyCache: proxyCache,
		hasDataDir: hasDataDir,
		group:      coalesce.New(),
		log:        log,
	}
}

func (p *proxyHelper) manifest(ctx context.Context) (*mojang.Manifest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Now().Before(p.cached.expiry) {
		return p.cached.manifest, nil
	}
	data, err := p.client.Get(ctx, constants.MojangManifest)
	if err != nil {
		return nil, err
	}
	var manifest mojang.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	p.cached = &manifestCache{manifest: &manifest, expiry: time.Now().Add(manifestCacheTTL)}
	return p.cached.manifest, nil
}

func (p *proxyHelper) versionJSON(ctx context.Context, id string) ([]byte, error) {
	manifest, err := p.manifest(ctx)
	if err != nil {
		return nil, err
	}
	url, err := mojang.ManifestVersionURL(manifest, id)
	if err != nil {
		return nil, err
	}
	return p.getBytes(ctx, url)
}

func (p *proxyHelper) jarTarget(ctx context.Context, kind, id string) (string, integrity.Spec, error) {
	raw, err := p.versionJSON(ctx, id)
	if err != nil {
		return "", integrity.Spec{}, err
	}
	url, spec, err := mojang.DownloadInfoFromVersionJSON(raw, kind)
	if err != nil {
		return "", integrity.Spec{}, err
	}
	return url, spec, nil
}

func (p *proxyHelper) assetIndex(ctx context.Context, file string) ([]byte, string, error) {
	indexID := stringsTrimSuffix(file, ".json")
	manifest, err := p.manifest(ctx)
	if err != nil {
		return nil, "", err
	}
	if raw, dest, err := p.assetIndexFromVersion(ctx, indexID); err == nil {
		return raw, dest, nil
	}
	for i, v := range manifest.Versions {
		if i >= 40 {
			break
		}
		raw, err := p.getBytes(ctx, v.URL)
		if err != nil {
			continue
		}
		url, err := mojang.AssetIndexUpstreamURL(raw)
		if err != nil {
			continue
		}
		var doc struct {
			AssetIndex struct {
				ID string `json:"id"`
			} `json:"assetIndex"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil || doc.AssetIndex.ID != indexID {
			continue
		}
		data, err := p.getBytes(ctx, url)
		if err != nil {
			return nil, "", err
		}
		return data, p.layout.AssetIndexPath(indexID), nil
	}
	return nil, "", fmt.Errorf("asset index %q not found upstream", file)
}

func (p *proxyHelper) assetIndexFromVersion(ctx context.Context, versionID string) ([]byte, string, error) {
	raw, err := p.versionJSON(ctx, versionID)
	if err != nil {
		return nil, "", err
	}
	url, err := mojang.AssetIndexUpstreamURL(raw)
	if err != nil {
		return nil, "", err
	}
	data, err := p.getBytes(ctx, url)
	if err != nil {
		return nil, "", err
	}
	var doc struct {
		AssetIndex struct {
			ID string `json:"id"`
		} `json:"assetIndex"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "", err
	}
	return data, p.layout.AssetIndexPath(doc.AssetIndex.ID), nil
}

func (p *proxyHelper) fabricMeta(ctx context.Context, rel string) ([]byte, error) {
	return p.getBytes(ctx, fabric.MetaURL(rel))
}

func (p *proxyHelper) getBytes(ctx context.Context, url string) ([]byte, error) {
	return p.group.Do(ctx, url, func(ctx context.Context) ([]byte, error) {
		// The leader's cancellation should not abort a fetch other clients
		// are still waiting on; the HTTP client timeout still bounds it.
		return p.client.Get(context.WithoutCancel(ctx), url)
	})
}

// download fetches url into dest on disk, coalesced per destination by the
// fetch client's per-file lock so concurrent requests share one upstream
// transfer. Callers serve dest from disk after it returns.
func (p *proxyHelper) download(ctx context.Context, url, dest string, spec integrity.Spec) error {
	if spec.Known() {
		return p.client.DownloadVerified(ctx, url, dest, spec)
	}
	return p.client.Download(ctx, url, dest)
}

// streamCache relays an upstream response to the client while writing the
// same bytes into the proxy cache. The client receives data immediately on a
// cache miss instead of waiting for the mirror-side download to finish.
// Client disconnects do not abort the upstream transfer; completing it seeds
// the cache for the next request.
func (p *proxyHelper) streamCache(ctx context.Context, url, dest string, spec integrity.Spec, w http.ResponseWriter, r *http.Request) (int64, error) {
	unlock := p.client.Lock(dest)
	defer unlock()

	fsys, err := p.layout.FS()
	if err != nil {
		return 0, err
	}
	if spec.Known() {
		if err := integrity.VerifyFile(fsys, dest, spec); err == nil {
			return 0, errAlreadyCached
		}
	} else if fsys.Exists(dest) {
		return 0, errAlreadyCached
	}

	resp, err := p.client.Do(context.WithoutCancel(ctx), url, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return 0, fmt.Errorf("upstream %s", resp.Status)
	}

	tmp := dest + constants.FilePartSuffix
	_ = fsys.Remove(tmp)
	if err := fsys.MkdirAll(filepath.Dir(tmp), safepath.DirPerm); err != nil {
		return 0, err
	}
	out, err := fsys.Create(tmp)
	if err != nil {
		return 0, err
	}

	h := w.Header()
	for _, k := range []string{constants.HeaderContentType, constants.HeaderContentLength} {
		if v := resp.Header.Get(k); v != "" {
			h[k] = []string{v}
		}
	}
	h[constants.HeaderAcceptRanges] = constants.AcceptRanges
	w.WriteHeader(http.StatusOK)

	var sent int64
	clientGone := r.Method == http.MethodHead
	copyErr := error(nil)
	buf := make([]byte, 256<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			written := 0
			for written < len(chunk) {
				m, writeErr := out.Write(chunk[written:])
				written += m
				if writeErr != nil {
					copyErr = writeErr
					break
				}
			}
			if copyErr != nil {
				break
			}
			if !clientGone {
				m, writeErr := w.Write(chunk)
				sent += int64(m)
				if writeErr != nil {
					clientGone = true
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			copyErr = readErr
			break
		}
	}
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = fsys.Remove(tmp)
		return sent, copyErr
	}
	if err := integrity.VerifyFile(fsys, tmp, spec); err != nil {
		_ = fsys.Remove(tmp)
		return sent, err
	}
	if err := fsys.Rename(tmp, dest); err != nil {
		_ = fsys.Remove(tmp)
		return sent, err
	}
	return sent, nil
}

var errAlreadyCached = fmt.Errorf("already cached")

// stream relays an upstream response directly to w, forwarding the client's
// Range header so partial downloads work without buffering the whole file.
func (p *proxyHelper) stream(ctx context.Context, url string, w http.ResponseWriter, r *http.Request) (int64, error) {
	fwd := http.Header{}
	if rg := r.Header.Get(constants.HeaderRange); rg != "" {
		fwd.Set(constants.HeaderRange, rg)
	}
	resp, err := p.client.Do(ctx, url, fwd)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("upstream %s", resp.Status)
	}
	h := w.Header()
	for _, k := range []string{
		constants.HeaderContentType,
		constants.HeaderContentLength,
		constants.HeaderContentRange,
	} {
		if v := resp.Header.Get(k); v != "" {
			h[k] = []string{v}
		}
	}
	h[constants.HeaderAcceptRanges] = constants.AcceptRanges
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return 0, nil
	}
	n, _ := io.Copy(w, resp.Body)
	return n, nil
}

func (p *proxyHelper) maybeCache(path string, data []byte, spec integrity.Spec) error {
	if !p.proxyCache || path == "" {
		return nil
	}
	if spec.Known() {
		if err := integrity.VerifyBytes(data, spec); err != nil {
			return err
		}
	}
	if err := store.WriteBytes(p.layout, path, data); err != nil {
		return err
	}
	if p.log != nil {
		p.log.Info("proxy cached", "path", path)
	}
	return nil
}

func stringsTrimSuffix(s, suffix string) string {
	if len(suffix) > 0 && len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}
