package archive

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"minecraft-version-archive/internal/constants"
	"minecraft-version-archive/internal/fabric"
	"minecraft-version-archive/internal/store"
)

type FabricOptions struct {
	MCVersions    []string
	LoaderVersion string
	AllLoaders    bool
}

func (a *Archiver) RunFabric(ctx context.Context, opts FabricOptions) error {
	if err := a.layout.Ensure(); err != nil {
		return err
	}

	mcVersions, err := a.resolveFabricMCVersions(opts.MCVersions)
	if err != nil {
		return err
	}
	if len(mcVersions) == 0 {
		return fmt.Errorf("no minecraft versions to archive; archive vanilla first or pass version arguments")
	}

	if len(mcVersions) == 1 {
		a.log.Info("fabric archiving version", "version", mcVersions[0])
	} else {
		a.log.Info("fabric archiving versions", "versions", len(mcVersions))
	}

	workers := a.cfg.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(mcVersions) {
		workers = len(mcVersions)
	}
	a.log.Info("fabric archive plan",
		"minecraft", len(mcVersions),
		"all_loaders", opts.AllLoaders,
		"workers", workers,
	)

	prog := NewProgress(len(mcVersions), a.log)
	go prog.Run(ctx)

	if err := a.archiveFabricIndex(ctx, "v2/versions/loader", prog); err != nil {
		return err
	}
	if err := a.archiveFabricIndex(ctx, "v2/versions/intermediary", prog); err != nil {
		return err
	}

	jobs := make(chan string, len(mcVersions))
	for _, mc := range mcVersions {
		jobs <- mc
	}
	close(jobs)

	var loaderTotal atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, len(mcVersions))
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for mc := range jobs {
				if ctx.Err() != nil {
					return
				}
				prog.BeginVersion(mc)
				count, err := a.archiveFabricMC(ctx, mc, opts, prog)
				prog.EndVersion(mc, err == nil)
				if err != nil {
					errCh <- fmt.Errorf("%s: %w", mc, err)
					continue
				}
				loaderTotal.Add(int64(count))
			}
		}()
	}
	wg.Wait()
	close(errCh)

	var firstErr error
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
		a.log.Error("fabric archive failed", "err", err)
	}
	prog.logStatus(true)
	if firstErr != nil {
		return firstErr
	}
	a.log.Info("fabric archive complete", "minecraft", len(mcVersions), "loaders", loaderTotal.Load())
	return nil
}

// ensureFabricArchive fills in missing Fabric metadata without re-downloading
// every version on each refresh. If the global loader list changes, all
// versions are refreshed because their recommended stable loader may have
// changed. Otherwise only versions with missing loader metadata are archived.
func (a *Archiver) ensureFabricArchive(ctx context.Context) error {
	mcVersions, err := a.resolveFabricMCVersions(nil)
	if err != nil {
		return err
	}
	if len(mcVersions) == 0 {
		return nil
	}

	refreshAll, err := a.fabricLoaderIndexChanged(ctx)
	if err != nil {
		a.log.Warn("fabric archive check failed", "err", err)
		return nil
	}
	todo := make([]string, 0, len(mcVersions))
	for _, mc := range mcVersions {
		if refreshAll || a.fabricMCNeedsArchive(mc) {
			todo = append(todo, mc)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	a.log.Info("fabric archive required", "versions", len(todo), "loader_index_changed", refreshAll)
	if err := a.RunFabric(ctx, FabricOptions{MCVersions: todo}); err != nil {
		a.log.Warn("fabric archive failed", "err", err)
	}
	return nil
}

func (a *Archiver) fabricLoaderIndexChanged(ctx context.Context) (bool, error) {
	rel := "v2/versions/loader"
	data, err := a.fetch.Get(ctx, fabric.MetaURL(rel))
	if err != nil {
		return false, err
	}
	path := a.layout.FabricMetaPath(fabric.MetaStorageRel(rel))
	old, err := a.layout.ReadFile(path)
	if err != nil {
		return true, nil
	}
	return !bytes.Equal(old, data), nil
}

func (a *Archiver) fabricMCNeedsArchive(mc string) bool {
	enc := fabric.EncodeGameVersion(mc)
	listRel := "v2/versions/loader/" + enc
	data, err := a.layout.ReadFile(a.layout.FabricMetaPath(fabric.MetaStorageRel(listRel)))
	if err != nil {
		return true
	}
	pairs, err := fabric.ParseLoaderPairs(data)
	if err != nil || len(pairs) == 0 {
		return false
	}
	for _, loader := range selectFabricLoaders(pairs, FabricOptions{}) {
		detailRel := listRel + "/" + fabric.EncodeGameVersion(loader)
		if !a.layout.Exists(a.layout.FabricMetaPath(fabric.MetaStorageRel(detailRel))) {
			return true
		}
	}
	return false
}

func (a *Archiver) resolveFabricMCVersions(requested []string) ([]string, error) {
	if len(requested) > 0 {
		return requested, nil
	}
	entries, err := a.layout.ReadDir(a.layout.VersionsDir())
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(ent.Name(), ".json"))
	}
	return out, nil
}

func (a *Archiver) archiveFabricIndex(ctx context.Context, rel string, prog *Progress) error {
	return a.archiveFabricMeta(ctx, rel, prog)
}

func (a *Archiver) archiveFabricMC(ctx context.Context, mc string, opts FabricOptions, prog *Progress) (int, error) {
	enc := fabric.EncodeGameVersion(mc)
	listRel := "v2/versions/loader/" + enc
	listData, err := a.fetchMeta(ctx, listRel, prog)
	if err != nil {
		return 0, fmt.Errorf("loader list: %w", err)
	}

	pairs, err := fabric.ParseLoaderPairs(listData)
	if err != nil {
		return 0, fmt.Errorf("parse loader list: %w", err)
	}
	if len(pairs) == 0 {
		a.log.Info("fabric skip", "minecraft", mc, "reason", "no compatible loaders")
		return 0, nil
	}

	loaders := selectFabricLoaders(pairs, opts)
	for _, loader := range loaders {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if err := a.archiveFabricLoader(ctx, mc, enc, loader, prog); err != nil {
			return 0, err
		}
	}
	return len(loaders), nil
}

func selectFabricLoaders(pairs []fabric.LoaderPair, opts FabricOptions) []string {
	if opts.LoaderVersion != "" {
		return []string{opts.LoaderVersion}
	}
	if opts.AllLoaders {
		out := make([]string, 0, len(pairs))
		for _, p := range pairs {
			if p.Loader.Version != "" {
				out = append(out, p.Loader.Version)
			}
		}
		return out
	}
	for _, p := range pairs {
		if p.Loader.Stable && p.Loader.Version != "" {
			return []string{p.Loader.Version}
		}
	}
	if pairs[0].Loader.Version != "" {
		return []string{pairs[0].Loader.Version}
	}
	return nil
}

func (a *Archiver) archiveFabricLoader(ctx context.Context, mc, encMC, loader string, prog *Progress) error {
	encLoader := fabric.EncodeGameVersion(loader)
	detailRel := "v2/versions/loader/" + encMC + "/" + encLoader
	detailData, err := a.fetchMeta(ctx, detailRel, prog)
	if err != nil {
		return fmt.Errorf("loader %s: %w", loader, err)
	}

	for _, suffix := range []string{"profile/json", "server/json"} {
		rel := detailRel + "/" + suffix
		if err := a.archiveFabricMeta(ctx, rel, prog); err != nil {
			a.log.Debug("fabric profile skip", "minecraft", mc, "loader", loader, "path", suffix, "err", err)
		}
	}

	detail, err := fabric.ParseLoaderDetail(detailData)
	if err != nil {
		return err
	}
	for _, lib := range fabric.LibrariesFromDetail(detail) {
		if err := a.archiveFabricLibrary(ctx, lib, prog); err != nil {
			return err
		}
	}
	if err := a.archiveFabricLoaderJSON(ctx, loader, prog); err != nil {
		a.log.Debug("fabric loader json skip", "loader", loader, "err", err)
	}
	return nil
}

func (a *Archiver) fetchMeta(ctx context.Context, rel string, prog *Progress) ([]byte, error) {
	prog.AddTasks(1)
	data, err := a.fetch.Get(ctx, fabric.MetaURL(rel))
	if err != nil {
		prog.TaskDone(0)
		return nil, err
	}
	if err := storeFabricMeta(a.layout, rel, data); err != nil {
		prog.TaskDone(0)
		return nil, err
	}
	prog.TaskDone(int64(len(data)))
	return data, nil
}

func (a *Archiver) archiveFabricMeta(ctx context.Context, rel string, prog *Progress) error {
	_, err := a.fetchMeta(ctx, rel, prog)
	return err
}

func (a *Archiver) archiveFabricLibrary(ctx context.Context, lib fabric.Library, prog *Progress) error {
	a.fabricArtifactMu.Lock()
	defer a.fabricArtifactMu.Unlock()
	if lib.Name == "" {
		return nil
	}
	base := lib.URL
	if base == "" {
		base = constants.FabricMavenBase + "/"
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	rel := fabric.MavenRelPath(lib.Name)
	if rel == "" {
		return nil
	}
	dest := a.layout.FabricMavenPath(rel)
	prog.AddTasks(1)
	if a.layout.Exists(dest) {
		prog.TaskDone(a.fileSize(dest))
		return nil
	}
	url := fabric.MavenDownloadURL(base, lib.Name)
	if url == "" {
		prog.TaskDone(0)
		return nil
	}
	if err := a.fetch.Download(ctx, url, dest); err != nil {
		prog.TaskDone(0)
		return err
	}
	prog.TaskDone(a.fileSize(dest))
	return nil
}

func (a *Archiver) archiveFabricLoaderJSON(ctx context.Context, loaderVersion string, prog *Progress) error {
	a.fabricArtifactMu.Lock()
	defer a.fabricArtifactMu.Unlock()
	coord := "net.fabricmc:fabric-loader:" + loaderVersion + ":json"
	rel := fabric.MavenRelPath(coord)
	if rel == "" {
		return fmt.Errorf("invalid loader coord")
	}
	dest := a.layout.FabricMavenPath(rel)
	prog.AddTasks(1)
	if a.layout.Exists(dest) {
		prog.TaskDone(a.fileSize(dest))
		return nil
	}
	url := fabric.MavenDownloadURL("https://maven.fabricmc.net/", coord)
	if err := a.fetch.Download(ctx, url, dest); err != nil {
		prog.TaskDone(0)
		return err
	}
	prog.TaskDone(a.fileSize(dest))
	return nil
}

func storeFabricMeta(layout *store.Layout, rel string, data []byte) error {
	path := layout.FabricMetaPath(fabric.MetaStorageRel(rel))
	return store.WriteBytes(layout, path, data)
}
