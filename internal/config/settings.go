package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Settings is the user-editable configuration for membox. It is persisted as
// TOML in the config directory (e.g. ~/.config/mm/config.toml).
type Settings struct {
	Perkeep PerkeepSettings `toml:"perkeep"`
	Tunnel  TunnelSettings  `toml:"tunnel"`
}

// PerkeepSettings describes how to reach the Perkeep server.
type PerkeepSettings struct {
	// Server is the full URL of the Perkeep server, e.g.
	// http://127.0.0.1:3179 or https://perkeep.example.com.
	// When an SSH tunnel is used, this field is usually left empty so the
	// daemon uses the local forwarded endpoint.
	Server string `toml:"server"`
}

// TunnelSettings describes the SSH tunnel used to reach a remote Perkeep.
type TunnelSettings struct {
	// Enabled turns the SSH tunnel on or off.
	Enabled bool `toml:"enabled"`
	// RemoteHost is the SSH host to connect to, e.g. 38.207.176.66.
	RemoteHost string `toml:"remote_host"`
	// RemotePort is the port on the remote loopback interface to forward.
	RemotePort int `toml:"remote_port"`
	// SSHPort is the remote SSH daemon port.
	SSHPort int `toml:"ssh_port"`
	// User is the remote SSH user.
	User string `toml:"user"`
	// PrivateKey is the optional path to an SSH private key file. If empty,
	// ssh-agent or default keys are used.
	PrivateKey string `toml:"private_key"`
	// LocalPort is the local port that receives the forwarded traffic.
	LocalPort int `toml:"local_port"`
}

// DefaultSettings returns a settings struct with sensible defaults.
func DefaultSettings() *Settings {
	return &Settings{
		Perkeep: PerkeepSettings{},
		Tunnel: TunnelSettings{
			Enabled:    true,
			RemotePort: 3179,
			SSHPort:    22,
			LocalPort:  3179,
		},
	}
}

// SettingsPath returns the path to the config file.
func SettingsPath(cfg *Config) string {
	return filepath.Join(cfg.ConfigDir, "config.toml")
}

// LoadSettings reads the user config file. If it does not exist, a default
// (empty) settings object is returned without error.
func LoadSettings(cfg *Config) (*Settings, error) {
	path := SettingsPath(cfg)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultSettings(), nil
		}
		return nil, fmt.Errorf("read settings file %q: %w", path, err)
	}

	var s Settings
	if err := toml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse settings file %q: %w", path, err)
	}

	// Expand ~ in paths.
	s.Tunnel.PrivateKey = expandPath(s.Tunnel.PrivateKey)
	return &s, nil
}

// SaveSettings persists the settings to the config directory.
func SaveSettings(cfg *Config, s *Settings) error {
	path := SettingsPath(cfg)
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	var b strings.Builder
	b.WriteString("# membox daemon configuration\n")
	b.WriteString("#\n")
	b.WriteString("# If tunnel.enabled is true, the daemon will open an SSH tunnel and\n")
	b.WriteString("# use the local forwarded port as the Perkeep server.\n")
	b.WriteString("# Otherwise set perkeep.server to the full URL of your Perkeep server.\n")
	b.WriteString("\n")

	enc := toml.NewEncoder(&b)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write settings file %q: %w", path, err)
	}
	return nil
}

// IsComplete reports whether the settings have enough information to reach a
// Perkeep server.
func (s *Settings) IsComplete() bool {
	if strings.TrimSpace(s.Perkeep.Server) != "" {
		return true
	}
	if s.Tunnel.Enabled {
		return strings.TrimSpace(s.Tunnel.RemoteHost) != "" &&
			strings.TrimSpace(s.Tunnel.User) != ""
	}
	return false
}

// EffectivePerkeepServer returns the Perkeep server URL the daemon should use.
// If the user explicitly set perkeep.server, that wins. Otherwise, if an SSH
// tunnel is enabled, the local forwarded endpoint is used.
func (s *Settings) EffectivePerkeepServer() string {
	if server := strings.TrimSpace(s.Perkeep.Server); server != "" {
		return server
	}
	if s.Tunnel.Enabled {
		return fmt.Sprintf("http://127.0.0.1:%d", s.Tunnel.LocalPort)
	}
	return ""
}

// PromptSettings interactively asks the user for missing configuration. It
// modifies the provided settings in place.
func PromptSettings(s *Settings) error {
	sc := bufio.NewScanner(os.Stdin)
	prompt := func(question, defaultValue string) string {
		if defaultValue != "" {
			fmt.Printf("%s [%s]: ", question, defaultValue)
		} else {
			fmt.Printf("%s: ", question)
		}
		if !sc.Scan() {
			return defaultValue
		}
		v := strings.TrimSpace(sc.Text())
		if v == "" {
			return defaultValue
		}
		return v
	}
	promptBool := func(question string, defaultValue bool) bool {
		d := "y"
		if !defaultValue {
			d = "n"
		}
		for {
			fmt.Printf("%s (y/n) [%s]: ", question, d)
			if !sc.Scan() {
				return defaultValue
			}
			switch strings.ToLower(strings.TrimSpace(sc.Text())) {
			case "":
				return defaultValue
			case "y", "yes":
				return true
			case "n", "no":
				return false
			}
			fmt.Println("Please answer y or n.")
		}
	}
	promptInt := func(question string, defaultValue int) int {
		for {
			fmt.Printf("%s [%d]: ", question, defaultValue)
			if !sc.Scan() {
				return defaultValue
			}
			v := strings.TrimSpace(sc.Text())
			if v == "" {
				return defaultValue
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				fmt.Println("Please enter a number.")
				continue
			}
			return n
		}
	}

	useTunnel := promptBool("Use an SSH tunnel to reach Perkeep?", s.Tunnel.Enabled)
	s.Tunnel.Enabled = useTunnel

	if useTunnel {
		s.Tunnel.RemoteHost = prompt("Remote SSH host", s.Tunnel.RemoteHost)
		s.Tunnel.SSHPort = promptInt("Remote SSH port", s.Tunnel.SSHPort)
		s.Tunnel.User = prompt("Remote SSH user", s.Tunnel.User)
		s.Tunnel.RemotePort = promptInt("Remote Perkeep port", s.Tunnel.RemotePort)
		s.Tunnel.LocalPort = promptInt("Local forwarded port", s.Tunnel.LocalPort)
		key := prompt("SSH private key path (optional)", s.Tunnel.PrivateKey)
		s.Tunnel.PrivateKey = expandPath(key)
	} else {
		s.Perkeep.Server = prompt("Perkeep server URL (e.g. http://127.0.0.1:3179)", s.Perkeep.Server)
	}

	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

// IsTerminal reports whether stdin is a character device (interactive terminal).
func IsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func expandPath(p string) string {
	if p == "" || !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
