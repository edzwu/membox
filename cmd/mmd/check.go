package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/earendil-works/membox/internal/config"
	"github.com/earendil-works/membox/internal/tunnel"
)

// runCheck prints the resolved configuration and tests whether the Perkeep
// server (optionally reached via an SSH tunnel) is reachable.
// If upload is true, it also writes a small test file through pk-put.
func runCheck(upload bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := config.InitWorkspace(cfg); err != nil {
		return fmt.Errorf("init workspace: %w", err)
	}

	fmt.Printf("config dir: %s\n", cfg.ConfigDir)
	fmt.Printf("data dir:   %s\n", cfg.DataDir)

	settings, err := config.LoadSettings(cfg)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if !settings.IsComplete() {
		return fmt.Errorf("config incomplete: %s", config.SettingsPath(cfg))
	}

	fmt.Printf("settings:   %s\n", config.SettingsPath(cfg))

	if home, err := os.UserHomeDir(); err == nil {
		fmt.Printf("perkeep client config: %s\n", filepath.Join(home, ".config", "perkeep", "client-config.json"))
	}

	printEnv("MM_PERKEEP_SERVER")
	printEnv("MM_TUNNEL_HOST")
	printEnv("MM_TUNNEL_PORT")
	printEnv("MM_TUNNEL_USER")
	printEnv("MM_TUNNEL_REMOTE_PORT")
	printEnv("MM_TUNNEL_LOCAL_PORT")
	printEnv("MM_TUNNEL_KEY")

	server := os.Getenv("MM_PERKEEP_SERVER")
	if server == "" {
		server = settings.EffectivePerkeepServer()
	}

	if settings.Tunnel.Enabled {
		tunnelCfg := &tunnel.Config{
			Host:       settings.Tunnel.RemoteHost,
			User:       settings.Tunnel.User,
			SSHPort:    settings.Tunnel.SSHPort,
			RemotePort: settings.Tunnel.RemotePort,
			LocalPort:  settings.Tunnel.LocalPort,
			PrivateKey: settings.Tunnel.PrivateKey,
		}
		fmt.Printf("tunnel config: %s@%s:%d -> 127.0.0.1:%d (remote port %d)\n",
			tunnelCfg.User, tunnelCfg.Host, tunnelCfg.SSHPort, tunnelCfg.LocalPort, tunnelCfg.RemotePort)
		if server == "" {
			server = fmt.Sprintf("http://127.0.0.1:%d", tunnelCfg.LocalPort)
		}

		ctx := context.Background()
		mgr := tunnel.NewManager(*tunnelCfg)
		if err := mgr.Start(ctx); err != nil {
			return fmt.Errorf("start tunnel: %w", err)
		}
		defer mgr.Stop()

		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := mgr.WaitReady(readyCtx); err != nil {
			return fmt.Errorf("tunnel not ready: %w", err)
		}
		fmt.Println("tunnel: ready")
	} else {
		fmt.Println("tunnel: not configured")
		if server == "" {
			server = "http://127.0.0.1:3179"
			fmt.Println("perkeep server: no perkeep.server set; defaulting to", server)
		}
	}

	if server == "" {
		server = "http://127.0.0.1:3179"
	}
	fmt.Printf("perkeep server: %s\n", server)

	// TCP reachability.
	hostPort := server
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		hostPort = u.Host
	}
	conn, err := net.DialTimeout("tcp", hostPort, 2*time.Second)
	if err != nil {
		return fmt.Errorf("tcp dial %s: %w", hostPort, err)
	}
	_ = conn.Close()
	fmt.Printf("tcp: %s reachable\n", hostPort)

	// HTTP status endpoint.
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(server + "/status.json")
	if err != nil {
		return fmt.Errorf("http status.json: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status.json returned %s\n%s", resp.Status, string(body))
	}
	fmt.Printf("http: status.json ok (%s)\n", resp.Status)
	preview := string(body)
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	fmt.Printf("status.json body:\n%s\n", preview)

	if _, err := exec.LookPath("pk-put"); err != nil {
		fmt.Println("pk-put: not found in PATH")
	} else {
		fmt.Println("pk-put: found in PATH")
	}
	if _, err := exec.LookPath("pk"); err != nil {
		fmt.Println("pk: not found in PATH")
	} else {
		fmt.Println("pk: found in PATH")
	}

	if upload {
		if err := runUploadCheck(server); err != nil {
			return err
		}
	}

	return nil
}

func printEnv(name string) {
	if v := os.Getenv(name); v != "" {
		fmt.Printf("%s=%s\n", name, v)
	}
}

func runUploadCheck(server string) error {
	if _, err := exec.LookPath("pk-put"); err != nil {
		return fmt.Errorf("pk-put not in PATH; cannot run upload check")
	}

	tmp, err := os.CreateTemp("", "mmd-check-*.md")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := fmt.Fprintln(tmp, "mmd connectivity check"); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	args := []string{
		fmt.Sprintf("-server=%s", server),
		"file",
		"--permanode",
		"--title=mmd-check",
		tmp.Name(),
	}
	cmd := exec.Command("pk-put", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pk-put upload failed: %w\n%s", err, string(out))
	}
	fmt.Printf("upload check: ok\npermanode: %s\n", strings.TrimSpace(string(out)))
	return nil
}
