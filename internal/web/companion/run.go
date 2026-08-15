package companion

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"membox/internal/agent"
	"membox/internal/bootstrap"
	"membox/internal/translation"
	"membox/internal/web"
	"membox/internal/web/backend"
)

// DefaultPort is the preferred fixed port so the browser extension keeps a
// stable pairing URL across companion restarts.
const DefaultPort = 8787

// Options configure one foreground companion run.
type Options struct {
	Home      string
	Port      int // 0 prefers DefaultPort, then falls back to ephemeral
	Lifecycle string
	Version   string // written to bridge.json for extension compatibility
	// OnReady fires once the server listens, before Run blocks (CLI output).
	OnReady func(baseURL, token string)
}

// Run executes the Web Companion in the current process: singleton lock,
// HTTP server, bridge.json, controller leases, and graceful shutdown. It is the
// shared core of `mm web run` (detached child) and `mm serve` (foreground).
func Run(ctx context.Context, options Options) error {
	if strings.TrimSpace(options.Home) == "" {
		return errors.New("membox home is required")
	}
	lifecycle := options.Lifecycle
	if lifecycle != LifecycleSession && lifecycle != LifecycleKeep {
		return fmt.Errorf("lifecycle must be %q or %q", LifecycleSession, LifecycleKeep)
	}
	release, err := AcquireLock(options.Home)
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			if status, probeErr := Probe(ctx, options.Home); probeErr == nil && status.Running {
				return fmt.Errorf("%w: %s", ErrAlreadyRunning, status.BaseURL)
			}
		}
		return err
	}
	defer release()

	service, err := bootstrap.Open(filepath.Join(options.Home, "membox.db"))
	if err != nil {
		return err
	}
	defer service.Close()

	token, err := backend.LoadOrCreateBridgeToken(options.Home)
	if err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopOnce := sync.OnceFunc(cancel)

	// Lifecycle may flip at runtime. The lease watcher reads mode under this
	// mutex on every tick; keep never depends on controller leases.
	var modeMu sync.Mutex
	currentMode := lifecycle
	server := web.NewServer(service)
	server.SetToken(token)
	server.SetTranslationStreamer(translation.MMDClient{
		SocketPath: translation.SocketPath(options.Home),
		Ensure: func(ctx context.Context) error {
			return translation.EnsureMMD(ctx, options.Home)
		},
	})
	server.ConfigureCompanion(lifecycle, stopOnce, func(mode string) {
		modeMu.Lock()
		currentMode = mode
		modeMu.Unlock()
		rewriteBridgeMode(options.Home, mode)
	})

	// Agent manager is lazy: first status/session request probes Pi.
	// Free-form agent.* keys live in settings without the typed TUI settingSpecs.
	storeGet := func(ctx context.Context, key string) (string, error) {
		return service.Store().GetSetting(ctx, key)
	}
	agentEnabled := true
	if v, _ := storeGet(runCtx, "agent.enabled"); v == "false" {
		agentEnabled = false
	}
	maxWorkers := 2
	if v, _ := storeGet(runCtx, "agent.max_workers"); v != "" {
		if n, convErr := strconv.Atoi(v); convErr == nil && n > 0 {
			maxWorkers = n
		}
	}
	idleTimeout := 10 * time.Minute
	if v, _ := storeGet(runCtx, "agent.idle_timeout"); v != "" {
		if d, convErr := time.ParseDuration(v); convErr == nil && d > 0 {
			idleTimeout = d
		}
	}
	piPath, _ := storeGet(runCtx, "agent.pi_path")
	writeTools := false
	if v, _ := storeGet(runCtx, "agent.write_tools"); v == "true" || v == "1" {
		writeTools = true
	}
	var catalog agent.SessionCatalog
	if c, ok := service.Store().(agent.SessionCatalog); ok {
		catalog = c
	}
	var baseURLOnce string
	agentMgr := agent.NewManager(agent.Config{
		Home:            options.Home,
		Enabled:         agentEnabled,
		PiPath:          piPath,
		MaxWorkers:      maxWorkers,
		IdleTimeout:     idleTimeout,
		WriteTools:      writeTools,
		ExtensionSource: agent.ExtensionSource,
		Catalog:         catalog,
		Tools:           &agent.ServiceDocumentTools{Service: service},
		GetSetting:      storeGet,
		InternalBaseURL: func() string { return baseURLOnce },
	})
	server.SetAgentManager(agentMgr)

	baseURL, listenPort, err := startWithPortPreference(runCtx, server, options.Port)
	if err != nil {
		_ = agentMgr.Shutdown(runCtx)
		return err
	}
	baseURLOnce = baseURL
	if _, err := backend.WriteBridgeFile(options.Home, backend.BridgeFile{
		BaseURL:     baseURL,
		Token:       token,
		Port:        listenPort,
		Mode:        lifecycle,
		PID:         os.Getpid(),
		HostVersion: options.Version,
	}); err != nil {
		_ = server.Shutdown(runCtx)
		return err
	}
	if options.OnReady != nil {
		options.OnReady(baseURL, token)
	}

	go watchControllerLeases(runCtx, server, 1500*time.Millisecond, func() bool {
		modeMu.Lock()
		defer modeMu.Unlock()
		return currentMode == LifecycleSession
	}, stopOnce)

	<-runCtx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// startWithPortPreference binds to the requested port; port 0 tries the
// DefaultPort first so extension pairing survives restarts.
func startWithPortPreference(ctx context.Context, server *web.Server, port int) (string, int, error) {
	attempts := []int{port}
	if port == 0 {
		attempts = []int{DefaultPort, 0}
	}
	var lastErr error
	for _, candidate := range attempts {
		baseURL, err := server.Start(ctx, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		listenPort := 0
		if parsed, parseErr := url.Parse(baseURL); parseErr == nil {
			listenPort, _ = strconv.Atoi(parsed.Port())
		}
		return baseURL, listenPort, nil
	}
	if lastErr == nil {
		lastErr = errors.New("failed to start web companion")
	}
	return "", 0, lastErr
}

// watchControllerLeases stops a session companion after all TUI controllers
// release or expire. A short startup grace lets the spawning TUI receive the
// ready response and register its first lease. Keep mode never consults leases.
func watchControllerLeases(ctx context.Context, server *web.Server, interval time.Duration, sessionMode func() bool, stop func()) {
	startedAt := time.Now()
	const startupGrace = 15 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sessionMode() {
				continue
			}
			active, ever := server.ControllerLeaseState()
			if active == 0 && (ever || time.Since(startedAt) >= startupGrace) {
				stop()
				return
			}
		}
	}
}

// rewriteBridgeMode keeps bridge.json honest after a runtime lifecycle switch.
func rewriteBridgeMode(home, mode string) {
	bridge, err := backend.ReadBridgeFile(home)
	if err != nil {
		return
	}
	bridge.Mode = mode
	_, _ = backend.WriteBridgeFile(home, bridge)
}
