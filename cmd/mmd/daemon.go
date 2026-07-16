package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

func runDaemon() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := config.InitWorkspace(cfg); err != nil {
		return fmt.Errorf("init workspace: %w", err)
	}

	settings, err := loadSettingsInteractive(cfg)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	fmt.Printf("mmd daemon starting\n")
	fmt.Printf("config dir: %s\n", cfg.ConfigDir)
	fmt.Printf("data dir:   %s\n", cfg.DataDir)
	fmt.Printf("settings:   %s\n", config.SettingsPath(cfg))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	perkeepServer := settings.EffectivePerkeepServer()

	// Optional SSH tunnel management. If the settings enable a tunnel, mmd owns
	// the tunnel lifecycle and restarts it automatically when it drops.
	var tunnelMgr *tunnel.Manager
	if settings.Tunnel.Enabled {
		tunnelCfg := &tunnel.Config{
			Host:       settings.Tunnel.RemoteHost,
			User:       settings.Tunnel.User,
			SSHPort:    settings.Tunnel.SSHPort,
			RemotePort: settings.Tunnel.RemotePort,
			LocalPort:  settings.Tunnel.LocalPort,
			PrivateKey: settings.Tunnel.PrivateKey,
		}
		if perkeepServer == "" {
			perkeepServer = fmt.Sprintf("http://127.0.0.1:%d", tunnelCfg.LocalPort)
		}

		tunnelMgr = tunnel.NewManager(*tunnelCfg)
		if err := tunnelMgr.Start(ctx); err != nil {
			return fmt.Errorf("start tunnel: %w", err)
		}
		defer tunnelMgr.Stop()

		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := tunnelMgr.WaitReady(readyCtx); err != nil {
			cancel()
			return fmt.Errorf("tunnel not ready: %w", err)
		}
		cancel()
		log.Printf("mmd: tunnel ready (127.0.0.1:%d)", tunnelCfg.LocalPort)
	}

	if perkeepServer == "" {
		log.Printf("mmd: warning: no perkeep server configured")
	} else {
		log.Printf("mmd: perkeep server: %s", perkeepServer)
	}

	db, err := sqlite.Open(filepath.Join(cfg.DataDir, "membox.sqlite"))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	ws, err := workspace.Load(cfg)
	if err != nil {
		return fmt.Errorf("load workspace: %w", err)
	}
	if err := ws.Save(cfg); err != nil {
		return fmt.Errorf("save workspace: %w", err)
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
	runSync(ctx, svc, syncRepo, cacheRoot, perkeepServer, tunnelMgr)

	for {
		select {
		case <-ticker.C:
			runSync(ctx, svc, syncRepo, cacheRoot, perkeepServer, tunnelMgr)
		case <-ctx.Done():
			fmt.Println("\nmmd: shutting down")
			return nil
		}
	}
}

func runSync(ctx context.Context, svc *service.NoteService, syncRepo *sqliterepo.PerkeepSyncRepo, cacheRoot, perkeepServer string, tunnelMgr *tunnel.Manager) {
	if tunnelMgr != nil && !tunnelMgr.IsHealthy() {
		log.Printf("mmd: tunnel not healthy, waiting for restart...")
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := tunnelMgr.WaitReady(readyCtx); err != nil {
			log.Printf("mmd: tunnel still not ready, skipping sync: %v", err)
			return
		}
	}

	res, err := svc.SyncToPerkeep(ctx, syncRepo, cacheRoot, perkeepServer)
	if err != nil {
		log.Printf("mmd: perkeep sync failed: %v", err)
		return
	}
	log.Printf("mmd: synced %d uploaded, %d skipped, %d failed", res.Uploaded, res.Skipped, res.Failed)
}

func loadSettingsInteractive(cfg *config.Config) (*config.Settings, error) {
	settings, err := config.LoadSettings(cfg)
	if err != nil {
		return nil, err
	}
	if settings.IsComplete() {
		return settings, nil
	}

	path := config.SettingsPath(cfg)
	if !config.IsTerminal() {
		return nil, fmt.Errorf("config incomplete: %s", path)
	}

	fmt.Printf("Config is incomplete (%s). Edit now? (y/n): ", path)
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "y", "yes":
			if err := config.PromptSettings(settings); err != nil {
				return nil, err
			}
			if err := config.SaveSettings(cfg, settings); err != nil {
				return nil, err
			}
			fmt.Printf("Saved settings to %s\n", path)
			return settings, nil
		case "n", "no":
			fmt.Printf("Edit %s manually and run mmd again.\n", path)
			os.Exit(1)
		default:
			fmt.Print("Please answer y or n: ")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("config incomplete: %s", path)
}
