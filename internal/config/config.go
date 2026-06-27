// Package config resolves membox runtime directories.
//
// Directory resolution follows the XDG Base Directory Specification by default:
//   - config: $XDG_CONFIG_HOME/mm/ or ~/.config/mm/
//   - data:   $XDG_DATA_HOME/mm/   or ~/.local/share/mm/
//
// For local development inside a Git repository, set MM_DEV=1. This overlays
// both directories onto ./.membox/config/ and ./.membox/data/ under the
// current working directory. The Makefile sets this automatically for `make run`.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	appName      = "mm"
	devMarkerDir = ".membox"
)

// Config holds resolved runtime paths and preferences.
type Config struct {
	ConfigDir string // directory holding app configuration (e.g. backends.toml)
	DataDir   string // directory holding runtime data (db, cache, indexes)
}

// Load resolves the runtime configuration.
//
// In dev mode (MM_DEV=1) config and data live under ./.membox in the current
// working directory. Otherwise XDG base directories are used.
//
// Load does not create directories; use InitWorkspace to ensure they exist.
func Load() (*Config, error) {
	configDir, dataDir, err := resolveDirs()
	if err != nil {
		return nil, err
	}

	return &Config{
		ConfigDir: configDir,
		DataDir:   dataDir,
	}, nil
}

// resolveDirs picks the config and data directories based on MM_DEV.
func resolveDirs() (configDir, dataDir string, err error) {
	if os.Getenv("MM_DEV") == "1" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("get working directory: %w", err)
		}
		base := filepath.Join(cwd, devMarkerDir)
		return filepath.Join(base, "config"), filepath.Join(base, "data"), nil
	}

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("resolve home directory: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("resolve home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}

	return filepath.Join(configHome, appName), filepath.Join(dataHome, appName), nil
}

// InitWorkspace ensures the runtime directories described by cfg exist,
// creating them if necessary.
func InitWorkspace(cfg *Config) error {
	for _, dir := range []string{cfg.ConfigDir, cfg.DataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}
