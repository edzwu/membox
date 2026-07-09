package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/earendil-works/membox/internal/config"
	fsrepo "github.com/earendil-works/membox/internal/repository/filesystem"
	sqliterepo "github.com/earendil-works/membox/internal/repository/sqlite"
	"github.com/earendil-works/membox/internal/service"
	"github.com/earendil-works/membox/internal/storage/sqlite"
	"github.com/earendil-works/membox/internal/tunnel"
	"github.com/earendil-works/membox/internal/workspace"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("mmd: load config: %v", err)
	}
	if err := config.InitWorkspace(cfg); err != nil {
		log.Fatalf("mmd: init workspace: %v", err)
	}

	fmt.Printf("mmd daemon starting\n")
	fmt.Printf("config dir: %s\n", cfg.ConfigDir)
	fmt.Printf("data dir:   %s\n", cfg.DataDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional SSH tunnel management. If MM_TUNNEL_HOST is set, mmd owns the
	// tunnel lifecycle and restarts it automatically when it drops.
	var tunnelMgr *tunnel.Manager
	if tunnelCfg, ok := tunnel.ConfigFromEnv(); ok {
		tunnelMgr = tunnel.NewManager(*tunnelCfg)
		if err := tunnelMgr.Start(ctx); err != nil {
			log.Fatalf("mmd: start tunnel: %v", err)
		}
		defer tunnelMgr.Stop()

		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := tunnelMgr.WaitReady(readyCtx); err != nil {
			cancel()
			log.Fatalf("mmd: tunnel not ready: %v", err)
		}
		cancel()
		log.Printf("mmd: tunnel ready (127.0.0.1:%d)", tunnelCfg.LocalPort)
	}

	db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
	if err != nil {
		log.Fatalf("mmd: open db: %v", err)
	}
	defer db.Close()

	ws, err := workspace.Load(cfg)
	if err != nil {
		log.Fatalf("mmd: load workspace: %v", err)
	}
	if err := ws.Save(cfg); err != nil {
		log.Fatalf("mmd: save workspace: %v", err)
	}

	repo := sqliterepo.NewNoteMetaRepo(db.DB())
	syncRepo := sqliterepo.NewPerkeepSyncRepo(db.DB())
	store := fsrepo.NewNoteStore(ws.Notes)
	svc := service.NewNoteService(repo, store, os.Getenv("EDITOR"))

	cacheRoot := filepath.Join(cfg.DataDir, "perkeep-cache")

	// Run an initial metadata sync so the local SQLite index reflects the
	// filesystem before pushing to Perkeep.
	if _, err := svc.Sync(context.Background()); err != nil {
		log.Printf("mmd: initial metadata sync failed: %v", err)
	}

	// Default sync interval: 60 seconds. Can be overridden by MM_SYNC_INTERVAL.
	interval := 60 * time.Second
	if v := os.Getenv("MM_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		} else {
			log.Printf("mmd: invalid MM_SYNC_INTERVAL %q, using default: %v", v, err)
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run one sync immediately at startup.
	runSync(ctx, svc, syncRepo, cacheRoot, tunnelMgr)

	for {
		select {
		case <-ticker.C:
			runSync(ctx, svc, syncRepo, cacheRoot, tunnelMgr)
		case <-ctx.Done():
			fmt.Println("\nmmd: shutting down")
			return
		}
	}
}

func runSync(ctx context.Context, svc *service.NoteService, syncRepo *sqliterepo.PerkeepSyncRepo, cacheRoot string, tunnelMgr *tunnel.Manager) {
	if tunnelMgr != nil && !tunnelMgr.IsHealthy() {
		log.Printf("mmd: tunnel not healthy, waiting for restart...")
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := tunnelMgr.WaitReady(readyCtx); err != nil {
			log.Printf("mmd: tunnel still not ready, skipping sync: %v", err)
			return
		}
	}

	res, err := svc.SyncToPerkeep(ctx, syncRepo, cacheRoot)
	if err != nil {
		log.Printf("mmd: perkeep sync failed: %v", err)
		return
	}
	log.Printf("mmd: synced %d uploaded, %d skipped, %d failed", res.Uploaded, res.Skipped, res.Failed)
}
