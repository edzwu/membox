// Package tunnel manages an SSH local port-forward tunnel so that mmd can keep
// a Perkeep server reachable without requiring the user to run ssh manually.
//
// It shells out to the system ssh(1) binary, which means the user's
// ~/.ssh/config, keys, and ssh-agent are used automatically. It deliberately
// does not support password authentication; use keys or agent instead.
package tunnel

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config describes the SSH tunnel endpoint.
type Config struct {
	// Host is the remote SSH host (e.g. "38.207.176.66").
	Host string
	// User is the remote SSH user (e.g. "edward").
	User string
	// SSHPort is the remote SSH daemon port (default 22).
	SSHPort int
	// RemotePort is the port on the remote loopback interface to forward (e.g. 3179).
	RemotePort int
	// LocalPort is the local port that receives the tunnel (e.g. 3179).
	LocalPort int
	// PrivateKey is an optional path to an SSH private key file.
	PrivateKey string
	// ExtraArgs are extra arguments passed verbatim to ssh.
	ExtraArgs []string
}

// ConfigFromEnv builds a tunnel config from environment variables. It returns
// nil if MM_TUNNEL_HOST is not set.
//
// Supported variables:
//   MM_TUNNEL_HOST      - remote host (required)
//   MM_TUNNEL_USER      - remote user (default: current user)
//   MM_TUNNEL_PORT      - remote SSH port (default: 22)
//   MM_TUNNEL_REMOTE_PORT - remote service port (default: 3179)
//   MM_TUNNEL_LOCAL_PORT  - local forwarded port (default: 3179)
//   MM_TUNNEL_KEY       - path to private key (optional)
//   MM_TUNNEL_EXTRA_ARGS - extra ssh args, space separated (optional)
func ConfigFromEnv() (*Config, bool) {
	host := os.Getenv("MM_TUNNEL_HOST")
	if host == "" {
		return nil, false
	}

	cfg := &Config{
		Host:       host,
		User:       os.Getenv("MM_TUNNEL_USER"),
		SSHPort:    envInt("MM_TUNNEL_PORT", 22),
		RemotePort: envInt("MM_TUNNEL_REMOTE_PORT", 3179),
		LocalPort:  envInt("MM_TUNNEL_LOCAL_PORT", 3179),
		PrivateKey: os.Getenv("MM_TUNNEL_KEY"),
	}
	if cfg.User == "" {
		u := os.Getenv("USER")
		if u == "" {
			u = "root"
		}
		cfg.User = u
	}
	if extra := os.Getenv("MM_TUNNEL_EXTRA_ARGS"); extra != "" {
		cfg.ExtraArgs = strings.Fields(extra)
	}
	return cfg, true
}

func envInt(name string, defaultVal int) int {
	v := os.Getenv(name)
	if v == "" {
		return defaultVal
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return defaultVal
}

// Manager keeps an SSH tunnel alive, restarting it when it dies.
type Manager struct {
	cfg    Config
	mu     sync.Mutex
	cmd    *exec.Cmd
	stop   chan struct{}
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewManager creates a tunnel manager for the given config.
func NewManager(cfg Config) *Manager {
	return &Manager{
		cfg:  cfg,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// Start opens the tunnel and begins monitoring it. It returns once the first
// ssh process has been launched; the tunnel may not be ready yet. Use WaitReady
// or IsHealthy before sending traffic.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return fmt.Errorf("tunnel already started")
	}

	m.wg.Add(1)
	go m.run(ctx)
	return nil
}

// Stop terminates the ssh process and stops the restart loop.
func (m *Manager) Stop() error {
	close(m.stop)

	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		// Give ssh a moment to exit cleanly, then kill if necessary.
		select {
		case <-m.done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}

	m.wg.Wait()
	return nil
}

// IsHealthy reports whether the local port accepts TCP connections.
func (m *Manager) IsHealthy() bool {
	addr := fmt.Sprintf("127.0.0.1:%d", m.cfg.LocalPort)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// WaitReady blocks until the tunnel is healthy or the context is cancelled.
func (m *Manager) WaitReady(ctx context.Context) error {
	for {
		if m.IsHealthy() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (m *Manager) run(ctx context.Context) {
	defer m.wg.Done()
	defer close(m.done)

	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-m.stop:
			return
		default:
		}

		cmd := m.buildCmd()

		m.mu.Lock()
		m.cmd = cmd
		m.mu.Unlock()

		log.Printf("tunnel: connecting %s@%s:%d -> 127.0.0.1:%d", m.cfg.User, m.cfg.Host, m.cfg.SSHPort, m.cfg.LocalPort)
		err := cmd.Start()
		if err != nil {
			log.Printf("tunnel: failed to start ssh: %v", err)
			backoff = m.sleepBackoff(backoff, maxBackoff)
			continue
		}

		// Wait for ssh to exit or stop signal.
		waitDone := make(chan error, 1)
		go func() { waitDone <- cmd.Wait() }()

		select {
		case <-m.stop:
			if cmd.Process != nil {
				_ = cmd.Process.Signal(os.Interrupt)
			}
			return
		case err := <-waitDone:
			if err != nil {
				log.Printf("tunnel: ssh exited: %v", err)
			} else {
				log.Printf("tunnel: ssh exited unexpectedly")
			}
			backoff = m.sleepBackoff(backoff, maxBackoff)
		}
	}
}

func (m *Manager) buildCmd() *exec.Cmd {
	args := []string{
		"-N",
		"-L", fmt.Sprintf("%d:127.0.0.1:%d", m.cfg.LocalPort, m.cfg.RemotePort),
		"-p", strconv.Itoa(m.cfg.SSHPort),
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
	}
	if m.cfg.PrivateKey != "" {
		args = append(args, "-i", m.cfg.PrivateKey)
	}
	args = append(args, m.cfg.ExtraArgs...)
	args = append(args, fmt.Sprintf("%s@%s", m.cfg.User, m.cfg.Host))

	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func (m *Manager) sleepBackoff(current, max time.Duration) time.Duration {
	select {
	case <-m.stop:
	case <-time.After(current):
	}
	if current < max {
		current *= 2
		if current > max {
			current = max
		}
	}
	return current
}
