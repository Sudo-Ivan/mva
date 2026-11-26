package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"minecraft-version-archive/internal/constants"
	"minecraft-version-archive/internal/polymc"
)

// metaResponse is a cached PolyMC meta document: generated component indexes
// and version files are expensive to rebuild, so they live for a short TTL.
type metaResponse struct {
	body  []byte
	gz    []byte
	etag  string
	exp   time.Time
	found bool
}

func (s *Server) servePolyMCMeta(ctx context.Context, rel, mcVersion, publicURL string) (*metaResponse, error) {
	if rel == "" {
		rel = "index.json"
	}
	key := rel + "\x00" + mcVersion + "\x00" + publicURL
	if v, ok := s.metaResp.Load(key); ok {
		if ent := v.(*metaResponse); time.Now().Before(ent.exp) {
			return ent, nil
		}
	}
	ttl := time.Hour
	ent, err := s.metaGroup.Do(ctx, key, func(context.Context) (*metaResponse, error) {
		if v, ok := s.metaResp.Load(key); ok {
			if e := v.(*metaResponse); time.Now().Before(e.exp) {
				return e, nil
			}
		}
		var data []byte
		var ok bool
		var err error
		if rel == "" || rel == "index.json" {
			data, err = s.buildPolyMCIndex(ctx, publicURL)
			ok = err == nil
		} else {
			data, ok, err = polymc.ServeMeta(s.layout, rel, mcVersion, publicURL)
		}
		if err != nil {
			return nil, err
		}
		if !ok {
			return &metaResponse{exp: time.Now().Add(ttl)}, nil
		}
		e := &metaResponse{
			body:  data,
			etag:  makeETag(data),
			exp:   time.Now().Add(ttl),
			found: true,
		}
		if len(data) >= constants.GzipMinBytes {
			e.gz = gzipData(data)
		}
		s.metaResp.Store(key, e)
		if s.metaCount.Add(1) > constants.MetaCacheMaxEntries {
			s.metaResp.Clear()
			s.metaCount.Store(0)
		}
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	return ent, nil
}

type polyMCComponent struct {
	rel      string
	uid      string
	name     string
	optional bool
}

func polyMCComponents() []polyMCComponent {
	return []polyMCComponent{
		{rel: polymc.UIDMinecraft + "/index.json", uid: polymc.UIDMinecraft, name: "Minecraft"},
		{rel: polymc.UIDJava + "/index.json", uid: polymc.UIDJava, name: "Java Runtimes"},
		{rel: polymc.UIDFabricLoader + "/index.json", uid: polymc.UIDFabricLoader, name: "Fabric Loader", optional: true},
		{rel: polymc.UIDIntermediary + "/index.json", uid: polymc.UIDIntermediary, name: "Intermediary Mappings", optional: true},
		{rel: polymc.UIDLWJGL3 + "/index.json", uid: polymc.UIDLWJGL3, name: "LWJGL 3"},
		{rel: polymc.UIDLWJGL2 + "/index.json", uid: polymc.UIDLWJGL2, name: "LWJGL 2"},
	}
}

// buildPolyMCIndex builds the root index through the response cache. Besides
// avoiding duplicate generation work, this warms the component index entries
// that launchers request immediately after the root index.
func (s *Server) buildPolyMCIndex(ctx context.Context, publicURL string) ([]byte, error) {
	pkgs := []map[string]string{}
	for _, c := range polyMCComponents() {
		ent, err := s.servePolyMCMeta(ctx, c.rel, "", publicURL)
		if err != nil || !ent.found {
			if c.optional {
				pkgs = append(pkgs, map[string]string{"uid": c.uid, "name": c.name})
			}
			continue
		}
		sum := sha256.Sum256(ent.body)
		pkgs = append(pkgs, map[string]string{
			"uid":    c.uid,
			"name":   c.name,
			"sha256": hex.EncodeToString(sum[:]),
		})
	}
	return json.MarshalIndent(map[string]any{"formatVersion": 1, "packages": pkgs}, "", "    ")
}

func (s *Server) handlePolyMCMeta(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	mcVersion := r.URL.Query().Get("minecraft")
	publicURL := s.publicURL(r)
	ent, err := s.servePolyMCMeta(r.Context(), rel, mcVersion, publicURL)
	if err == nil && ent.found {
		w.Header()[constants.HeaderContentType] = constants.ContentTypeJSON
		s.emitTagged(w, r, ent.body, ent.gz, false, constants.CacheControlShort, ent.etag)
		return
	}
	if s.proxy != nil {
		data, ok, err := s.proxyPolyMCMeta(r.Context(), rel, mcVersion, publicURL)
		if err != nil {
			s.recordProxyError("polymc meta proxy", err, "path", rel, "minecraft", mcVersion)
			http.NotFound(w, r)
			return
		}
		if ok {
			w.Header()[constants.HeaderContentType] = constants.ContentTypeJSON
			s.emitTagged(w, r, data, nil, false, constants.CacheControlShort, "")
			return
		}
	}
	http.NotFound(w, r)
}
