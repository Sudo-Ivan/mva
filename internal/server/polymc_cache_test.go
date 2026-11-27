package server

import (
	"context"
	"os"
	"testing"

	"log/slog"
	"minecraft-version-archive/internal/archive"
	"minecraft-version-archive/internal/config"
	"minecraft-version-archive/internal/stats"
	"minecraft-version-archive/internal/store"
)

func TestPolyMCIndexWarmsComponentCaches(t *testing.T) {
	dir := t.TempDir()
	layout := store.New(dir)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	versionJSON := `{
		"id": "9.9.9", "type": "release", "releaseTime": "2024-01-01T00:00:00Z",
		"mainClass": "net.minecraft.client.main.Main", "javaVersion": {"component": "java-runtime-delta", "majorVersion": 21},
		"minecraftArguments": "--username ${auth_player_name}",
		"downloads": {"client": {"sha1": "aa", "size": 10, "url": "https://piston-data.mojang.com/x/client.jar"}},
		"assetIndex": {"id": "1", "sha1": "bb", "size": 1, "totalSize": 1, "url": "https://x/1.json"},
		"libraries": [{"name": "org.lwjgl:lwjgl:3.3.3", "downloads": {"artifact": {"sha1": "d", "size": 2, "url": "https://libraries.minecraft.net/org/lwjgl/lwjgl/3.3.3/lwjgl-3.3.3.jar"}}}]
	}`
	if err := os.WriteFile(layout.VersionJSON("9.9.9"), []byte(versionJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{DataDir: dir, PublicURL: "http://test", CacheEnabled: true, CacheEntries: 64, CacheBytes: 64 << 20}
	log := slog.New(slog.DiscardHandler)
	srv := New(cfg, archive.NewWithStats(cfg, log, stats.New()), log)
	if _, err := srv.servePolyMCMeta(context.Background(), "index.json", "", cfg.PublicURL); err != nil {
		t.Fatal(err)
	}

	key := "net.minecraft/index.json\x00\x00" + cfg.PublicURL
	if _, ok := srv.metaResp.Load(key); !ok {
		t.Fatalf("root index generation did not warm %q", key)
	}
}

func TestPolyMCMetaCachesMissingResponses(t *testing.T) {
	dir := t.TempDir()
	layout := store.New(dir)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DataDir: dir, PublicURL: "http://test", CacheEnabled: true, CacheEntries: 64, CacheBytes: 64 << 20}
	log := slog.New(slog.DiscardHandler)
	srv := New(cfg, archive.NewWithStats(cfg, log, stats.New()), log)

	ent, err := srv.servePolyMCMeta(context.Background(), "org.lwjgl3/not-archived.json", "", cfg.PublicURL)
	if err != nil {
		t.Fatal(err)
	}
	if ent.found {
		t.Fatal("missing component reported as found")
	}
	key := "org.lwjgl3/not-archived.json\x00\x00" + cfg.PublicURL
	cached, ok := srv.metaResp.Load(key)
	if !ok {
		t.Fatal("missing response was not cached")
	}
	if cached.(*metaResponse).found {
		t.Fatal("cached missing response marked found")
	}
}
