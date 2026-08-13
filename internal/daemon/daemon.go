package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"membox"
	"membox/internal/application"
	"membox/internal/bootstrap"
	"membox/internal/infrastructure/blobstore"
	"membox/internal/infrastructure/system"
)

const (
	socketName = "mmd.sock"
	pidName    = "mmd.pid"
	lockName   = "mmd.lock"
	logName    = "mmd.log"
)

var (
	ErrAlreadyRunning = errors.New("mmd is already running")
	ErrNotRunning     = errors.New("mmd is not running")
)

type Config struct {
	Home         string
	DatabasePath string
	ObjectRoot   string
	RuntimeDir   string
	SocketPath   string
	PIDPath      string
	LockPath     string
	LogPath      string
}

// DefaultConfig uses the same home resolution as mm so Pi tools and mmd
// always address one catalog unless the caller explicitly passes --home.
func DefaultConfig(home string) (Config, error) {
	if strings.TrimSpace(home) == "" {
		home = strings.TrimSpace(os.Getenv("MEMBOX_HOME"))
		if home == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return Config{}, fmt.Errorf("resolve user home: %w", err)
			}
			home = filepath.Join(userHome, ".membox")
		}
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return Config{}, fmt.Errorf("resolve mmd home: %w", err)
	}
	runtimeDir := filepath.Join(absolute, "mmd")
	return Config{
		Home:         absolute,
		DatabasePath: filepath.Join(absolute, "membox.db"),
		ObjectRoot:   filepath.Join(absolute, "objects"),
		RuntimeDir:   runtimeDir,
		SocketPath:   filepath.Join(runtimeDir, socketName),
		PIDPath:      filepath.Join(runtimeDir, pidName),
		LockPath:     filepath.Join(runtimeDir, lockName),
		LogPath:      filepath.Join(runtimeDir, logName),
	}, nil
}

type Health struct {
	Status   string    `json:"status"`
	PID      int       `json:"pid"`
	Version  string    `json:"version"`
	Database string    `json:"database"`
	Objects  string    `json:"objects"`
	Socket   string    `json:"socket"`
	Started  time.Time `json:"started_at"`
}

type ScanRequest struct {
	Selector        string `json:"selector,omitempty"`
	TimestampSource string `json:"timestamp_source,omitempty"`
}

type ScanReport struct {
	Paths               int    `json:"paths"`
	Files               int    `json:"files"`
	Added               int    `json:"added"`
	Updated             int    `json:"updated"`
	Renamed             int    `json:"renamed"`
	Unchanged           int    `json:"unchanged"`
	Missing             int    `json:"missing"`
	PossibleRenames     int    `json:"possible_renames"`
	Errors              int    `json:"errors"`
	TimestampSource     string `json:"timestamp_source,omitempty"`
	GitPaths            int    `json:"git_paths,omitempty"`
	TimestampsUpdated   int    `json:"timestamps_updated,omitempty"`
	TimestampsUnchanged int    `json:"timestamps_unchanged,omitempty"`
	NoGitHistory        int    `json:"no_git_history,omitempty"`
	NonGitPaths         int    `json:"non_git_paths,omitempty"`
}

type scanResponse struct {
	Report ScanReport `json:"report"`
	Error  string     `json:"error,omitempty"`
}

func apiScanReport(report application.ScanReport) ScanReport {
	return ScanReport{
		Paths: report.Paths, Files: report.Files, Added: report.Added, Updated: report.Updated,
		Renamed: report.Renamed, Unchanged: report.Unchanged, Missing: report.Missing,
		PossibleRenames: report.PossibleRenames, Errors: report.Errors,
		TimestampSource: report.TimestampSource, GitPaths: report.GitPaths,
		TimestampsUpdated: report.TimestampsUpdated, TimestampsUnchanged: report.TimestampsUnchanged,
		NoGitHistory: report.NoGitHistory, NonGitPaths: report.NonGitPaths,
	}
}

type Daemon struct {
	config   Config
	service  *application.Service
	listener net.Listener
	server   *http.Server
	instance *system.OSMutex
	log      *log.Logger
	health   Health

	shutdownOnce sync.Once
}

func New(config Config) *Daemon {
	return &Daemon{config: config}
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := d.prepareRuntime(); err != nil {
		return err
	}
	if err := d.acquireInstance(); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			d.cleanup()
		}
	}()

	service, err := bootstrap.Open(d.config.DatabasePath)
	if err != nil {
		return fmt.Errorf("open mmd database %s: %w", d.config.DatabasePath, err)
	}
	d.service = service
	objects, err := blobstore.Open(d.config.ObjectRoot)
	if err != nil {
		return fmt.Errorf("open mmd object store %s: %w", d.config.ObjectRoot, err)
	}
	service.SetContentStore(objects)

	if err := d.writePID(os.Getpid()); err != nil {
		return err
	}
	logFile, err := os.OpenFile(d.config.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open mmd log: %w", err)
	}
	defer logFile.Close()
	d.log = log.New(logFile, "mmd ", log.LstdFlags|log.Lmicroseconds)
	d.log.Printf("starting pid=%d database=%s socket=%s", os.Getpid(), d.config.DatabasePath, d.config.SocketPath)

	// A stale socket is safe to remove only after probing it. A live daemon
	// would have been rejected by acquireInstance above.
	if err := removeStaleSocket(d.config.SocketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", d.config.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on mmd socket %s: %w", d.config.SocketPath, err)
	}
	if err := os.Chmod(d.config.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("secure mmd socket: %w", err)
	}
	d.listener = listener
	d.health = Health{
		Status:   "ok",
		PID:      os.Getpid(),
		Version:  membox.Version,
		Database: d.config.DatabasePath,
		Objects:  d.config.ObjectRoot,
		Socket:   d.config.SocketPath,
		Started:  time.Now().UTC(),
	}

	d.server = &http.Server{Handler: d.handler()}
	go func() {
		<-ctx.Done()
		d.shutdown()
	}()

	d.log.Printf("ready")
	err = d.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	d.shutdown()
	d.log.Printf("stopped")
	cleanup = false
	d.cleanup()
	return err
}

func (d *Daemon) prepareRuntime() error {
	if err := os.MkdirAll(d.config.RuntimeDir, 0o700); err != nil {
		return fmt.Errorf("create mmd runtime directory: %w", err)
	}
	return nil
}

func (d *Daemon) acquireInstance() error {
	instance, err := system.OpenOSMutex(d.config.RuntimeDir, "mmd")
	if err != nil {
		return fmt.Errorf("open mmd instance lock: %w", err)
	}
	locked, err := instance.TryLock()
	if err != nil {
		_ = instance.Close()
		return fmt.Errorf("acquire mmd instance lock: %w", err)
	}
	if !locked {
		_ = instance.Close()
		return ErrAlreadyRunning
	}
	d.instance = instance
	return nil
}

func (d *Daemon) writePID(pid int) error {
	if err := os.WriteFile(d.config.PIDPath, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write mmd pid: %w", err)
	}
	return nil
}

func (d *Daemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, d.health)
	})
	mux.HandleFunc("POST /shutdown", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusAccepted, map[string]string{"status": "shutting_down"})
		// Shutdown must happen after this handler returns; net/http waits for
		// active handlers during Shutdown.
		go d.shutdown()
	})
	mux.HandleFunc("POST /v1/scan", func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
		var input ScanRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(writer, http.StatusBadRequest, scanResponse{Error: "invalid scan request: " + err.Error()})
			return
		}
		report, err := d.service.ScanPaths(request.Context(), application.ScanOptions{
			Selector: input.Selector, TimestampSource: input.TimestampSource,
		})
		response := scanResponse{Report: apiScanReport(report)}
		status := http.StatusOK
		if err != nil {
			response.Error = err.Error()
			status = http.StatusUnprocessableEntity
		}
		writeJSON(writer, status, response)
	})
	return mux
}

func (d *Daemon) shutdown() {
	d.shutdownOnce.Do(func() {
		if d.server == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = d.server.Shutdown(ctx)
	})
}

func (d *Daemon) cleanup() {
	if d.listener != nil {
		_ = d.listener.Close()
		d.listener = nil
	}
	if d.service != nil {
		_ = d.service.Close()
		d.service = nil
	}
	if d.instance != nil {
		_ = d.instance.Unlock()
		_ = d.instance.Close()
		d.instance = nil
	}
	_ = os.Remove(d.config.SocketPath)
	_ = os.Remove(d.config.PIDPath)
}

func removeStaleSocket(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect mmd socket: %w", err)
	}
	if err := probeSocket(path); err == nil {
		return ErrAlreadyRunning
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale mmd socket: %w", err)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func client(config Config) *http.Client {
	return &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", config.SocketPath)
			},
		},
	}
}

func probeSocket(path string) error {
	config := Config{SocketPath: path}
	response, err := client(config).Get("http://mmd/health")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func Healthcheck(config Config) (Health, error) {
	response, err := client(config).Get("http://mmd/health")
	if err != nil {
		return Health{}, ErrNotRunning
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Health{}, fmt.Errorf("health returned HTTP %d", response.StatusCode)
	}
	var health Health
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return Health{}, fmt.Errorf("decode health response: %w", err)
	}
	return health, nil
}

func Scan(ctx context.Context, config Config, request ScanRequest) (ScanReport, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return ScanReport{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mmd/v1/scan", strings.NewReader(string(body)))
	if err != nil {
		return ScanReport{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	httpClient := client(config)
	// A full scan or Git timestamp reconciliation can legitimately exceed the
	// short health-check timeout; the caller's context owns cancellation.
	httpClient.Timeout = 0
	response, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ScanReport{}, ctx.Err()
		}
		return ScanReport{}, ErrNotRunning
	}
	defer response.Body.Close()
	var result scanResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return ScanReport{}, fmt.Errorf("decode scan response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if result.Error != "" {
			return result.Report, errors.New(result.Error)
		}
		return result.Report, fmt.Errorf("scan returned HTTP %d", response.StatusCode)
	}
	return result.Report, nil
}

func Stop(ctx context.Context, config Config) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mmd/shutdown", nil)
	if err != nil {
		return err
	}
	response, err := client(config).Do(req)
	if err != nil {
		return ErrNotRunning
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("shutdown returned HTTP %d", response.StatusCode)
	}
	return nil
}

// WaitStopped waits until the daemon no longer answers health checks. It is
// used by the CLI so `mmd stop` does not report success while the database is
// still open.
func WaitStopped(ctx context.Context, config Config) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := Healthcheck(config); errors.Is(err, ErrNotRunning) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
