package archive

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"minecraft-version-archive/internal/config"
	"minecraft-version-archive/internal/constants"
	"minecraft-version-archive/internal/mojang"
	"minecraft-version-archive/internal/stats"
	"minecraft-version-archive/internal/store"
)

func TestArchiveAssetsSkipsInvalidHashes(t *testing.T) {
	const goodHash = "676452af5563894d3ff20f7cee2cc750a78de2fb"

	indexBody, err := json.Marshal(map[string]any{
		"objects": map[string]any{
			"bad":  map[string]any{"hash": "zz", "size": 1},
			"path": map[string]any{"hash": "../../../escape", "size": 1},
			"good": map[string]any{"hash": goodHash, "size": 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /files/index.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(indexBody)
	})
	mux.HandleFunc("GET /assets/67/"+goodHash, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("asset-data"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	origBase := constants.MojangAssetBase
	constants.MojangAssetBase = ts.URL + "/assets"
	t.Cleanup(func() { constants.MojangAssetBase = origBase })

	cfg := &config.Config{
		DataDir:     t.TempDir(),
		PublicURL:   "http://mirror.test",
		Workers:     2,
		RetryMax:    1,
		RetryBaseMS: 1,
		CacheBytes:  1,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := NewWithStats(cfg, log, stats.New())
	if err := a.layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	index, err := store.OpenHashIndex(a.layout)
	if err != nil {
		t.Fatal(err)
	}
	a.index = index

	version := &mojang.Version{
		AssetIndex: mojang.AssetIndexRef{
			ID:  "test-assets",
			URL: ts.URL + "/files/index.json",
		},
	}
	prog := NewProgress(1, log)
	if err := a.archiveAssets(context.Background(), version, prog); err != nil {
		t.Fatal(err)
	}

	layout := a.layout
	if !layout.Exists(layout.AssetObjectPath(goodHash)) {
		t.Fatal("valid asset object not archived")
	}
	if layout.Exists(layout.AssetObjectPath("zz")) {
		t.Fatal("asset with invalid hash was written")
	}
	if layout.Exists(layout.AssetObjectPath("../../../escape")) {
		t.Fatal("asset with traversal hash was written")
	}
	// One task for the asset index itself, then one per object.
	if got := prog.filesDone.Load(); got != 4 {
		t.Fatalf("filesDone = %d, want 4", got)
	}
}
