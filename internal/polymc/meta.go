package polymc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"minecraft-version-archive/internal/constants"
	"minecraft-version-archive/internal/fabric"
	"minecraft-version-archive/internal/store"
)

const (
	UIDFabricLoader   = "net.fabricmc.fabric-loader"
	UIDIntermediary   = "net.fabricmc.intermediary"
	FabricMavenPublic = "https://maven.fabricmc.net/"
)

type Require struct {
	UID      string `json:"uid"`
	Equals   string `json:"equals,omitempty"`
	Suggests string `json:"suggests,omitempty"`
}

type VersionEntry struct {
	Version     string    `json:"version"`
	Type        string    `json:"type"`
	ReleaseTime string    `json:"releaseTime"`
	Recommended bool      `json:"recommended"`
	Volatile    bool      `json:"volatile,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	Requires    []Require `json:"requires,omitempty"`
}

type VersionList struct {
	FormatVersion int            `json:"formatVersion"`
	UID           string         `json:"uid"`
	Name          string         `json:"name"`
	Versions      []VersionEntry `json:"versions"`
}

type Library struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type VersionFile struct {
	FormatVersion int       `json:"formatVersion"`
	UID           string    `json:"uid"`
	Version       string    `json:"version"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Order         int       `json:"order"`
	MainClass     string    `json:"mainClass,omitempty"`
	ReleaseTime   string    `json:"releaseTime"`
	Volatile      bool      `json:"volatile,omitempty"`
	Libraries     []Library `json:"libraries"`
	Requires      []Require `json:"requires"`
}

type component struct {
	uid, name string
	index     func() ([]byte, error)
}

// components lists every PolyMC component this mirror can serve. java/lwjgl
// entries generate from archived data and drop out when nothing is archived.
func components(layout *store.Layout, publicURL string) []component {
	return []component{
		{UIDMinecraft, "Minecraft", func() ([]byte, error) { return MinecraftIndex(layout, publicURL) }},
		{UIDJava, "Java Runtimes", func() ([]byte, error) { return JavaIndex(layout, publicURL) }},
		{UIDFabricLoader, "Fabric Loader", func() ([]byte, error) { return FabricLoaderIndex(layout, "", publicURL) }},
		{UIDIntermediary, "Intermediary Mappings", func() ([]byte, error) { return IntermediaryIndex(layout, publicURL) }},
		{UIDLWJGL3, "LWJGL 3", func() ([]byte, error) { return LWJGLIndex(layout, publicURL, UIDLWJGL3) }},
		{UIDLWJGL2, "LWJGL 2", func() ([]byte, error) { return LWJGLIndex(layout, publicURL, UIDLWJGL2) }},
	}
}

// IndexJSON builds the top-level package index. Each package carries the
// sha256 of its index.json like upstream meta, when the index generates.
func IndexJSON(layout *store.Layout, publicURL string) ([]byte, error) {
	pkgs := []map[string]string{}
	for _, c := range components(layout, publicURL) {
		body, err := c.index()
		if err != nil {
			// Fabric components are advertised even when empty for backward
			// compatibility; others only appear when data exists.
			if c.uid == UIDFabricLoader || c.uid == UIDIntermediary {
				pkgs = append(pkgs, map[string]string{"uid": c.uid, "name": c.name})
			}
			continue
		}
		sum := sha256.Sum256(body)
		pkgs = append(pkgs, map[string]string{
			"uid":    c.uid,
			"name":   c.name,
			"sha256": hex.EncodeToString(sum[:]),
		})
	}
	out := map[string]any{"formatVersion": 1, "packages": pkgs}
	return json.MarshalIndent(out, "", "    ")
}

func ServeMeta(layout *store.Layout, relPath, mcVersion, publicURL string) ([]byte, bool, error) {
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	switch relPath {
	case "", "index.json":
		data, err := IndexJSON(layout, publicURL)
		return data, true, err
	case UIDMinecraft + "/index.json":
		data, err := MinecraftIndex(layout, publicURL)
		return data, true, err
	case UIDLWJGL3 + "/index.json":
		data, err := LWJGLIndex(layout, publicURL, UIDLWJGL3)
		return data, true, err
	case UIDLWJGL2 + "/index.json":
		data, err := LWJGLIndex(layout, publicURL, UIDLWJGL2)
		return data, true, err
	case UIDJava + "/index.json":
		data, err := JavaIndex(layout, publicURL)
		return data, true, err
	case UIDFabricLoader + "/index.json":
		data, err := FabricLoaderIndex(layout, mcVersion, publicURL)
		return data, true, err
	case UIDIntermediary + "/index.json":
		data, err := IntermediaryIndex(layout, publicURL)
		return data, true, err
	}

	for _, uid := range []string{UIDMinecraft, UIDLWJGL3, UIDLWJGL2, UIDJava} {
		if strings.HasPrefix(relPath, uid+"/") && strings.HasSuffix(relPath, ".json") {
			version := strings.TrimSuffix(strings.TrimPrefix(relPath, uid+"/"), ".json")
			var data []byte
			var err error
			switch uid {
			case UIDMinecraft:
				data, err = MinecraftDetail(layout, version, publicURL)
			case UIDLWJGL3, UIDLWJGL2:
				data, err = LWJGLVersion(layout, publicURL, uid, version)
			case UIDJava:
				data, err = JavaVersion(layout, publicURL, version)
			}
			if err != nil {
				return nil, false, nil
			}
			return data, true, nil
		}
	}

	if strings.HasPrefix(relPath, UIDFabricLoader+"/") && strings.HasSuffix(relPath, ".json") {
		version := strings.TrimSuffix(strings.TrimPrefix(relPath, UIDFabricLoader+"/"), ".json")
		data, err := FabricLoaderVersion(layout, version, mcVersion, publicURL)
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	}

	if strings.HasPrefix(relPath, UIDIntermediary+"/") && strings.HasSuffix(relPath, ".json") {
		version := strings.TrimSuffix(strings.TrimPrefix(relPath, UIDIntermediary+"/"), ".json")
		data, err := IntermediaryVersion(layout, version, publicURL)
		return data, true, err
	}

	return nil, false, nil
}

func FabricLoaderIndex(layout *store.Layout, mcVersion, publicURL string) ([]byte, error) {
	if mcVersion != "" {
		return fabricLoaderIndexForMC(layout, mcVersion, publicURL)
	}
	return aggregateLoaderIndex(layout, publicURL)
}

func fabricLoaderIndexForMC(layout *store.Layout, mcVersion, publicURL string) ([]byte, error) {
	encMC := fabric.EncodeGameVersion(mcVersion)
	rel := "v2/versions/loader/" + encMC
	data, ok := readFabricMeta(layout, rel)
	if !ok {
		return nil, fmt.Errorf("no fabric loaders archived for %s", mcVersion)
	}
	pairs, err := fabric.ParseLoaderPairs(data)
	if err != nil {
		return nil, err
	}
	entries := make([]VersionEntry, 0, len(pairs))
	firstStable := true
	for _, pair := range pairs {
		loader := pair.Loader.Version
		if loader == "" {
			continue
		}
		if !fabricLoaderDetailExists(layout, encMC, loader) {
			continue
		}
		entry := VersionEntry{
			Version:  loader,
			Type:     "release",
			Requires: fabricLoaderRequires(mcVersion),
		}
		if pair.Loader.Stable && firstStable {
			entry.Recommended = true
			firstStable = false
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no fabric loaders archived for %s", mcVersion)
	}
	times := LoaderReleaseTimes(context.Background())
	applyReleaseTimes(entries, times)
	for i := range entries {
		detailRel := rel + "/" + fabric.EncodeGameVersion(entries[i].Version)
		raw, ok := readFabricMeta(layout, detailRel)
		if !ok {
			continue
		}
		detail, err := LoaderVersionFromDetailData(raw, entries[i].Version, mcVersion, publicURL, times)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(detail)
		entries[i].SHA256 = hex.EncodeToString(sum[:])
	}
	return marshalVersionList(UIDFabricLoader, "Fabric Loader", entries)
}

func fabricLoaderDetailExists(layout *store.Layout, encMC, loader string) bool {
	detailRel := "v2/versions/loader/" + encMC + "/" + fabric.EncodeGameVersion(loader)
	return layout.Exists(layout.FabricMetaPath(fabric.MetaStorageRel(detailRel)))
}

func LoaderIndexFromMCData(data []byte, mcVersion string) ([]byte, error) {
	return loaderIndexFromMCData(data, mcVersion, nil)
}

func LoaderIndexFromMCDataWithTimes(data []byte, mcVersion string, times map[string]string) ([]byte, error) {
	return loaderIndexFromMCData(data, mcVersion, times)
}

func loaderIndexFromMCData(data []byte, mcVersion string, times map[string]string) ([]byte, error) {
	pairs, err := fabric.ParseLoaderPairs(data)
	if err != nil {
		return nil, err
	}
	entries := make([]VersionEntry, 0, len(pairs))
	firstStable := true
	for _, pair := range pairs {
		entry := VersionEntry{
			Version:  pair.Loader.Version,
			Type:     "release",
			Requires: fabricLoaderRequires(mcVersion),
		}
		if pair.Loader.Stable && firstStable {
			entry.Recommended = true
			firstStable = false
		}
		entries = append(entries, entry)
	}
	applyReleaseTimes(entries, times)
	return marshalVersionList(UIDFabricLoader, "Fabric Loader", entries)
}

func fabricLoaderRequires(mcVersion string) []Require {
	_ = mcVersion
	return []Require{{UID: UIDIntermediary}}
}

func LoaderIndexFromGlobalData(data []byte) ([]byte, error) {
	return loaderIndexFromGlobalData(data, nil)
}

func LoaderIndexFromGlobalDataWithTimes(data []byte, times map[string]string) ([]byte, error) {
	return loaderIndexFromGlobalData(data, times)
}

func loaderIndexFromGlobalData(data []byte, times map[string]string) ([]byte, error) {
	var items []fabric.VersionEntry
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	entries := make([]VersionEntry, 0, len(items))
	firstStable := true
	for _, item := range items {
		entry := VersionEntry{
			Version:  item.Version,
			Type:     "release",
			Requires: []Require{{UID: UIDIntermediary}},
		}
		if item.Stable && firstStable {
			entry.Recommended = true
			firstStable = false
		}
		entries = append(entries, entry)
	}
	applyReleaseTimes(entries, times)
	return marshalVersionList(UIDFabricLoader, "Fabric Loader", entries)
}

func aggregateLoaderIndex(layout *store.Layout, publicURL string) ([]byte, error) {
	loaderDir := filepath.Join(layout.FabricMetaDir(), "v2", "versions", "loader", "_mc")
	entries, err := layout.ReadDir(loaderDir)
	if err != nil {
		return nil, fmt.Errorf("no fabric loader metadata archived")
	}
	seen := make(map[string]VersionEntry)
	mcByLoader := make(map[string]string)
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		mcFile := strings.TrimSuffix(ent.Name(), ".json")
		data, err := layout.ReadFile(filepath.Join(loaderDir, ent.Name()))
		if err != nil {
			continue
		}
		pairs, err := fabric.ParseLoaderPairs(data)
		if err != nil {
			continue
		}
		for _, pair := range pairs {
			loader := pair.Loader.Version
			if loader == "" {
				continue
			}
			if !fabricLoaderDetailExists(layout, mcFile, loader) {
				continue
			}
			if _, ok := seen[loader]; !ok {
				mcByLoader[loader] = mcFile
				seen[loader] = VersionEntry{
					Version:     loader,
					Type:        "release",
					Requires:    []Require{{UID: UIDIntermediary}},
					Recommended: pair.Loader.Stable,
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("no fabric loader metadata archived")
	}
	out := make([]VersionEntry, 0, len(seen))
	firstStable := true
	for _, entry := range seen {
		if entry.Recommended && firstStable {
			out = append(out, entry)
			firstStable = false
			continue
		}
		entry.Recommended = false
		out = append(out, entry)
	}
	times := LoaderReleaseTimes(context.Background())
	applyReleaseTimes(out, times)
	for i := range out {
		mc := mcByLoader[out[i].Version]
		if mc == "" {
			continue
		}
		detailRel := "v2/versions/loader/" + mc + "/" + fabric.EncodeGameVersion(out[i].Version)
		raw, ok := readFabricMeta(layout, detailRel)
		if !ok {
			continue
		}
		detail, err := LoaderVersionFromDetailData(raw, out[i].Version, mc, publicURL, times)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(detail)
		out[i].SHA256 = hex.EncodeToString(sum[:])
	}
	return marshalVersionList(UIDFabricLoader, "Fabric Loader", out)
}

func FabricLoaderVersion(layout *store.Layout, loaderVersion, mcVersion, publicURL string) ([]byte, error) {
	mcCandidates := fabricMCCandidates(layout, mcVersion)
	var lastErr error
	for _, mc := range mcCandidates {
		rel := "v2/versions/loader/" + fabric.EncodeGameVersion(mc) + "/" + loaderVersion
		data, ok := readFabricMeta(layout, rel)
		if !ok {
			continue
		}
		out, err := LoaderVersionFromDetailData(data, loaderVersion, mc, publicURL, LoaderReleaseTimes(context.Background()))
		if err != nil {
			lastErr = err
			continue
		}
		return out, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("fabric loader %s not archived", loaderVersion)
}

func LoaderVersionFromDetailData(data []byte, loaderVersion, mcVersion, publicURL string, times map[string]string) ([]byte, error) {
	detail, err := fabric.ParseLoaderDetail(data)
	if err != nil {
		return nil, err
	}
	return loaderDetailToVersionFile(detail, loaderVersion, mcVersion, publicURL, times)
}

func loaderDetailToVersionFile(detail fabric.LoaderDetail, loaderVersion, mcVersion, publicURL string, times map[string]string) ([]byte, error) {
	mavenBase := FabricMavenPublic
	if publicURL != "" {
		mavenBase = strings.TrimRight(publicURL, "/") + "/maven/fabricmc/"
	}
	libs := fabric.LibrariesFromDetail(detail)
	outLibs := make([]Library, 0, len(libs))
	for _, lib := range libs {
		url := lib.URL
		if url == "" || url == constants.FabricMavenBase || strings.HasPrefix(url, constants.FabricMavenBase) {
			url = mavenBase
		}
		outLibs = append(outLibs, Library{Name: lib.Name, URL: url})
	}
	file := VersionFile{
		FormatVersion: 1,
		UID:           UIDFabricLoader,
		Version:       loaderVersion,
		Name:          "Fabric Loader",
		Type:          "release",
		Order:         10,
		MainClass:     "net.fabricmc.loader.impl.launch.knot.KnotClient",
		ReleaseTime:   releaseTimeFor(loaderVersion, times),
		Libraries:     outLibs,
		Requires:      fabricLoaderRequires(mcVersion),
	}
	return json.MarshalIndent(file, "", "    ")
}

func IntermediaryIndex(layout *store.Layout, publicURL string) ([]byte, error) {
	entries, err := layout.ReadDir(layout.VersionsDir())
	if err != nil {
		return nil, err
	}
	out := make([]VersionEntry, 0)
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(ent.Name(), ".json")
		detail, err := IntermediaryVersion(layout, id, publicURL)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(detail)
		out = append(out, VersionEntry{
			Version:     id,
			Type:        "release",
			ReleaseTime: intermediaryReleaseTime(layout, id),
			Recommended: true,
			Volatile:    true,
			SHA256:      hex.EncodeToString(sum[:]),
			Requires:    []Require{{UID: "net.minecraft", Equals: id}},
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no minecraft versions archived")
	}
	return marshalVersionList(UIDIntermediary, "Intermediary Mappings", out)
}

func intermediaryReleaseTime(layout *store.Layout, version string) string {
	doc, err := readVersionDoc(layout, version)
	if err == nil {
		if releaseTime, ok := doc["releaseTime"].(string); ok && releaseTime != "" {
			return releaseTime
		}
	}
	return "1970-01-01T00:00:00Z"
}

func IntermediaryVersion(layout *store.Layout, version, publicURL string) ([]byte, error) {
	mavenBase := FabricMavenPublic
	if publicURL != "" {
		mavenBase = strings.TrimRight(publicURL, "/") + "/maven/fabricmc/"
	}
	file := VersionFile{
		FormatVersion: 1,
		UID:           UIDIntermediary,
		Version:       version,
		Name:          "Intermediary Mappings",
		Type:          "release",
		Order:         11,
		ReleaseTime:   intermediaryReleaseTime(layout, version),
		Volatile:      true,
		Libraries: []Library{
			{Name: "net.fabricmc:intermediary:" + version, URL: mavenBase},
		},
		Requires: []Require{{UID: "net.minecraft", Equals: version}},
	}
	return json.MarshalIndent(file, "", "    ")
}

func fabricMCCandidates(layout *store.Layout, mcVersion string) []string {
	if mcVersion != "" {
		return []string{mcVersion}
	}
	entries, err := layout.ReadDir(layout.VersionsDir())
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(ent.Name(), ".json"))
	}
	return out
}

func readFabricMeta(layout *store.Layout, rel string) ([]byte, bool) {
	path := layout.FabricMetaPath(fabric.MetaStorageRel(rel))
	data, err := layout.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func marshalVersionList(uid, name string, versions []VersionEntry) ([]byte, error) {
	for i := range versions {
		if versions[i].ReleaseTime == "" {
			versions[i].ReleaseTime = "1970-01-01T00:00:00Z"
		}
	}
	list := VersionList{
		FormatVersion: 1,
		UID:           uid,
		Name:          name,
		Versions:      versions,
	}
	return json.MarshalIndent(list, "", "    ")
}
