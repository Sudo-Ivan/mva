package polymc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minecraft-version-archive/internal/fabric"
	"minecraft-version-archive/internal/store"
)

func TestIntermediaryVersion(t *testing.T) {
	layout := writeTestVersion(t)
	data, err := IntermediaryVersion(layout, "1.21.1", "http://mirror")
	if err != nil {
		t.Fatal(err)
	}
	var file VersionFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.UID != UIDIntermediary || file.Version != "1.21.1" {
		t.Fatalf("unexpected version file: %+v", file)
	}
	if len(file.Libraries) != 1 || file.Libraries[0].URL != "http://mirror/maven/fabricmc/" {
		t.Fatalf("intermediary library not rewritten: %+v", file.Libraries)
	}
}

func TestFabricLoaderIndexFromPairs(t *testing.T) {
	root := t.TempDir()
	layout := store.New(root)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	pairs := `[{"loader":{"version":"0.16.9","stable":true},"intermediary":{"version":"1.21.1"}}]`
	path := layout.FabricMetaPath(fabric.MetaStorageRel("v2/versions/loader/1.21.1"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(pairs), 0o644); err != nil {
		t.Fatal(err)
	}
	detailPath := layout.FabricMetaPath(fabric.MetaStorageRel("v2/versions/loader/1.21.1/0.16.9"))
	if err := os.MkdirAll(filepath.Dir(detailPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(detailPath, []byte(`{"loader":{"version":"0.16.9"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := FabricLoaderIndex(layout, "1.21.1")
	if err != nil {
		t.Fatal(err)
	}
	var list VersionList
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Versions) != 1 || list.Versions[0].Version != "0.16.9" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestLoaderIndexFromMCData(t *testing.T) {
	raw := `[{"loader":{"version":"0.19.3","stable":true}}]`
	data, err := LoaderIndexFromMCData([]byte(raw), "26.2")
	if err != nil {
		t.Fatal(err)
	}
	var list VersionList
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Versions) != 1 || list.Versions[0].Version != "0.19.3" || !list.Versions[0].Recommended {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestFabricLoaderEndToEndUsesStorageLayout(t *testing.T) {
	root := t.TempDir()
	layout := store.New(root)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.VersionJSON("1.21.1")[:len(layout.VersionJSON("1.21.1"))-len("1.21.1.json")], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.VersionJSON("1.21.1"), []byte(`{"id":"1.21.1","releaseTime":"2024-08-08T14:40:46Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	pairs := `[{"loader":{"version":"0.18.4","stable":true,"maven":"net.fabricmc:fabric-loader:0.18.4"},"intermediary":{"version":"1.21.1","maven":"net.fabricmc:intermediary:1.21.1"}}]`
	listPath := layout.FabricMetaPath(fabric.MetaStorageRel("v2/versions/loader/1.21.1"))
	if err := os.MkdirAll(filepath.Dir(listPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listPath, []byte(pairs), 0o644); err != nil {
		t.Fatal(err)
	}

	detail := `{
		"loader":{"version":"0.18.4","stable":true,"maven":"net.fabricmc:fabric-loader:0.18.4"},
		"intermediary":{"version":"1.21.1","maven":"net.fabricmc:intermediary:1.21.1"},
		"launcherMeta":{"libraries":{"common":[{"name":"org.ow2.asm:asm:9.9","url":"https://maven.fabricmc.net/"}],"client":[],"server":[]}}
	}`
	detailPath := layout.FabricMetaPath(fabric.MetaStorageRel("v2/versions/loader/1.21.1/0.18.4"))
	if err := os.MkdirAll(filepath.Dir(detailPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(detailPath, []byte(detail), 0o644); err != nil {
		t.Fatal(err)
	}

	index, err := FabricLoaderIndex(layout, "1.21.1")
	if err != nil {
		t.Fatal(err)
	}
	var list VersionList
	if err := json.Unmarshal(index, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Versions) != 1 || list.Versions[0].Version != "0.18.4" {
		t.Fatalf("bad fabric index: %s", index)
	}

	versionData, err := FabricLoaderVersion(layout, "0.18.4", "", "http://mirror")
	if err != nil {
		t.Fatal(err)
	}
	var file VersionFile
	if err := json.Unmarshal(versionData, &file); err != nil {
		t.Fatal(err)
	}
	if file.UID != UIDFabricLoader || file.Version != "0.18.4" {
		t.Fatalf("bad fabric detail: %s", versionData)
	}
	names := make([]string, 0, len(file.Libraries))
	for _, lib := range file.Libraries {
		names = append(names, lib.Name)
		if lib.URL != "http://mirror/maven/fabricmc/" {
			t.Fatalf("library not rewritten: %+v", lib)
		}
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "org.ow2.asm:asm:9.9") || !strings.Contains(joined, "net.fabricmc:fabric-loader:0.18.4") || !strings.Contains(joined, "net.fabricmc:intermediary:1.21.1") {
		t.Fatalf("bad library names: %v", names)
	}

	intermediary, err := IntermediaryVersion(layout, "1.21.1", "http://mirror")
	if err != nil {
		t.Fatal(err)
	}
	var intermediaryFile VersionFile
	if err := json.Unmarshal(intermediary, &intermediaryFile); err != nil {
		t.Fatal(err)
	}
	if intermediaryFile.Libraries[0].Name != "net.fabricmc:intermediary:1.21.1" || intermediaryFile.Libraries[0].URL != "http://mirror/maven/fabricmc/" {
		t.Fatalf("bad intermediary: %s", intermediary)
	}
}
