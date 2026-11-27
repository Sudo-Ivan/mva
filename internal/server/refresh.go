package server

import (
	"context"
	"errors"
	"time"

	"minecraft-version-archive/internal/archive"
)

// StartAutoRefresh launches a background loop that re-runs the archiver every
// cfg.RefreshMin minutes so new Mojang releases are picked up without manual
// intervention. An immediate run happens on startup; later ticks are skipped
// while a run is still in progress.
func (s *Server) StartAutoRefresh(ctx context.Context) {
	interval := time.Duration(s.cfg.RefreshMin) * time.Minute
	if interval <= 0 {
		s.log.Warn("auto-refresh disabled, refresh_min must be positive", "refresh_min", s.cfg.RefreshMin)
		return
	}
	go func() {
		s.log.Info("auto-refresh enabled", "interval", interval)
		s.prewarmPolyMCMeta(ctx)
		s.refreshArchive(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.refreshArchive(ctx)
			}
		}
	}()
}

func (s *Server) refreshArchive(ctx context.Context) {
	err := s.archiver.TryRun(ctx)
	switch {
	case err == nil:
		s.invExpiry.Store(0)
		s.javaExpiry.Store(0)
		s.javaRespCache.Clear()
		s.metaResp.Clear()
		s.metaCount.Store(0)
		go s.prewarmPolyMCMeta(ctx)
	case errors.Is(err, archive.ErrInProgress):
		s.log.Debug("auto-refresh skipped, archive in progress")
	default:
		s.log.Warn("auto-refresh failed", "err", err)
	}
}

// prewarmPolyMCMeta generates the root PolyMC index in the background. Root
// generation also populates the component index cache entries used by
// launchers, so the first client does not pay the full cold-cache cost.
func (s *Server) prewarmPolyMCMeta(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if _, err := s.servePolyMCMeta(ctx, "index.json", "", s.cfg.PublicURL); err != nil {
		s.log.Debug("polymc meta prewarm failed", "err", err)
	}
}
