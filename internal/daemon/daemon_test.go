package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"membox/internal/infrastructure/sqlite"
)

func TestDefaultConfigMatchesMMHomeResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEMBOX_HOME", home)
	config, err := DefaultConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if config.Home != home {
		t.Fatalf("default home = %q, want %q", config.Home, home)
	}
	if filepath.Base(config.DatabasePath) != "membox.db" {
		t.Fatalf("database path = %q", config.DatabasePath)
	}
	if filepath.Base(config.SocketPath) != socketName {
		t.Fatalf("socket path = %q", config.SocketPath)
	}
}

func TestDaemonHealthAndGracefulStop(t *testing.T) {
	home := shortTempDir(t)
	config, err := DefaultConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- New(config).Run(ctx) }()

	var health Health
	waitFor(t, func() bool {
		select {
		case err := <-runErr:
			t.Fatalf("daemon exited before health check: %v", err)
		default:
		}
		var healthErr error
		health, healthErr = Healthcheck(config)
		return healthErr == nil
	})
	if health.Status != "ok" {
		t.Fatalf("health status = %q", health.Status)
	}
	if health.Database != config.DatabasePath || health.Socket != config.SocketPath {
		t.Fatalf("health paths = database %q socket %q", health.Database, health.Socket)
	}
	if _, err := os.Stat(config.DatabasePath); err != nil {
		t.Fatalf("daemon database missing: %v", err)
	}
	if _, err := os.Stat(config.SocketPath); err != nil {
		t.Fatalf("daemon socket missing: %v", err)
	}

	if err := Stop(context.Background(), config); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, func() bool {
		_, err := Healthcheck(config)
		return errors.Is(err, ErrNotRunning)
	})
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not exit")
	}
	if _, err := os.Stat(config.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after stop: %v", err)
	}
	if _, err := os.Stat(config.PIDPath); !os.IsNotExist(err) {
		t.Fatalf("pid file still exists after stop: %v", err)
	}
}

func TestScanAPIStoresBaselineAndChangedContentAsImmutableVersions(t *testing.T) {
	home := shortTempDir(t)
	workspace := t.TempDir()
	documentPath := filepath.Join(workspace, "article.md")
	firstBody := []byte("# Article\n\nVersion one.\n")
	if err := os.WriteFile(documentPath, firstBody, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := DefaultConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddOrReactivatePath(context.Background(), workspace, time.Now()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- New(config).Run(ctx) }()
	waitFor(t, func() bool {
		_, healthErr := Healthcheck(config)
		return healthErr == nil
	})

	report, err := Scan(context.Background(), config, ScanRequest{TimestampSource: "filesystem"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 1 || report.Files != 1 {
		t.Fatalf("first scan = %+v", report)
	}
	firstHash := sha256.Sum256(firstBody)
	firstHex := hex.EncodeToString(firstHash[:])
	firstObject := filepath.Join(config.ObjectRoot, "sha256", firstHex[:2], firstHex[2:])
	if stored, err := os.ReadFile(firstObject); err != nil || string(stored) != string(firstBody) {
		t.Fatalf("first object: body=%q err=%v", stored, err)
	}

	db, err := sql.Open("sqlite", config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertVersionCounts(t, db, 1, 1)

	secondBody := []byte("# Article\n\nVersion two is different.\n")
	if err := os.WriteFile(documentPath, secondBody, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err = Scan(context.Background(), config, ScanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("second scan = %+v", report)
	}
	secondHash := sha256.Sum256(secondBody)
	secondHex := hex.EncodeToString(secondHash[:])
	secondObject := filepath.Join(config.ObjectRoot, "sha256", secondHex[:2], secondHex[2:])
	if stored, err := os.ReadFile(secondObject); err != nil || string(stored) != string(secondBody) {
		t.Fatalf("second object: body=%q err=%v", stored, err)
	}
	assertVersionCounts(t, db, 2, 2)
	rows, err := db.Query(`SELECT id,parent_version_id,reason FROM document_versions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var firstID, secondID int64
	var firstParent, secondParent sql.NullInt64
	var firstReason, secondReason string
	if !rows.Next() || rows.Scan(&firstID, &firstParent, &firstReason) != nil {
		t.Fatal("missing first version")
	}
	if !rows.Next() || rows.Scan(&secondID, &secondParent, &secondReason) != nil {
		t.Fatal("missing second version")
	}
	if firstParent.Valid || firstReason != "initial_import" || !secondParent.Valid || secondParent.Int64 != firstID || secondReason != "external_edit" {
		rows.Close()
		t.Fatalf("version chain: first=%d/%v/%s second=%d/%v/%s", firstID, firstParent, firstReason, secondID, secondParent, secondReason)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Scan(context.Background(), config, ScanRequest{}); err != nil {
		t.Fatal(err)
	}
	assertVersionCounts(t, db, 2, 2)

	if err := Stop(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
}

func assertVersionCounts(t *testing.T, db *sql.DB, versions, contents int) {
	t.Helper()
	var gotVersions, gotContents, heads int
	if err := db.QueryRow(`SELECT COUNT(*) FROM document_versions`).Scan(&gotVersions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM contents`).Scan(&gotContents); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM document_heads`).Scan(&heads); err != nil {
		t.Fatal(err)
	}
	if gotVersions != versions || gotContents != contents || heads != 1 {
		t.Fatalf("versions=%d contents=%d heads=%d; want %d/%d/1", gotVersions, gotContents, heads, versions, contents)
	}
}

func TestConcurrentDaemonStartHasSingleOwner(t *testing.T) {
	config, err := DefaultConfig(shortTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- New(config).Run(ctx)
		}()
	}
	close(start)
	var loser error
	select {
	case loser = <-results:
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent daemon start did not reject one process")
	}
	if !errors.Is(loser, ErrAlreadyRunning) {
		t.Fatalf("losing daemon returned %v", loser)
	}
	waitFor(t, func() bool {
		_, healthErr := Healthcheck(config)
		return healthErr == nil
	})
	cancel()
	select {
	case winner := <-results:
		if winner != nil {
			t.Fatalf("winning daemon returned %v", winner)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("winning daemon did not stop")
	}
}

func TestHealthEndpointIsHTTPOverUnixSocket(t *testing.T) {
	home := shortTempDir(t)
	config, err := DefaultConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- New(config).Run(ctx) }()
	waitFor(t, func() bool {
		select {
		case err := <-runErr:
			t.Fatalf("daemon exited before health check: %v", err)
		default:
		}
		_, err := Healthcheck(config)
		return err == nil
	})

	response, err := client(config).Get("http://mmd/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	cancel()
	waitFor(t, func() bool {
		_, err := Healthcheck(config)
		return errors.Is(err, ErrNotRunning)
	})
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "mmd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return home
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
