package pdfconvert

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	ConfigFilename = "pdf-converter.json"
	ServerURLEnv   = "MEMBOX_PDF_CONVERTER_URL"
)

type Config struct {
	ServerURL string `json:"server_url"`
}

type ConfigStore struct {
	path string
}

func NewConfigStore(home string) ConfigStore {
	return ConfigStore{path: filepath.Join(home, ConfigFilename)}
}

func (s ConfigStore) Path() string { return s.path }

func (s ConfigStore) Load() (Config, error) {
	body, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{ServerURL: strings.TrimSpace(os.Getenv(ServerURLEnv))}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading PDF converter config: %w", err)
	}
	var config Config
	if err := json.Unmarshal(body, &config); err != nil {
		return Config{}, fmt.Errorf("decoding PDF converter config: %w", err)
	}
	config.ServerURL = strings.TrimRight(strings.TrimSpace(config.ServerURL), "/")
	if config.ServerURL != "" {
		if err := ValidateServerURL(config.ServerURL); err != nil {
			return Config{}, err
		}
	}
	return config, nil
}

func (s ConfigStore) SaveServerURL(serverURL string) (Config, error) {
	serverURL = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if err := ValidateServerURL(serverURL); err != nil {
		return Config{}, err
	}
	return s.save(Config{ServerURL: serverURL})
}

func (s ConfigStore) save(config Config) (Config, error) {
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Config{}, err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return Config{}, fmt.Errorf("creating PDF converter config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".pdf-converter-*.json")
	if err != nil {
		return Config{}, fmt.Errorf("creating PDF converter config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return Config{}, err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return Config{}, fmt.Errorf("writing PDF converter config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Config{}, err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return Config{}, fmt.Errorf("publishing PDF converter config: %w", err)
	}
	return config, nil
}

func ValidateServerURL(serverURL string) error {
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		return errors.New("PDF converter server URL is required")
	}
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid PDF converter server URL %q: use http://host:port", serverURL)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid PDF converter server URL %q: query and fragment are not allowed", serverURL)
	}
	return nil
}
