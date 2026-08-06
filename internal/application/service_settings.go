package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Service) Status(ctx context.Context) (port.StatusSnapshot, error) {
	return s.store.Status(ctx)
}

// Setting is one user-configurable option shown in the config panel.
type Setting struct {
	Key     string
	Label   string
	Value   string
	Options []string
}

// settingSpecs declares all configurable options. Adding a new setting only
// requires appending one entry here. Options may be empty for dynamic lists
// (see ListSettings).
var settingSpecs = []Setting{
	{Key: "viewer", Label: "viewer", Value: "leaf", Options: []string{"leaf", "web"}},
	{Key: "model", Label: "model", Value: "k3", Options: []string{"k3", "grok-4.5"}},
	{Key: SettingMainPath, Label: "main path", Value: "", Options: nil},
	{Key: SettingHideNotes, Label: "hide notes", Value: "on", Options: []string{"on", "off"}},
	{Key: SettingWebOnExit, Label: "web on exit", Value: OnExitAsk, Options: []string{OnExitAsk, OnExitStop, OnExitKeep}},
}

const (
	ViewerLeaf       = "leaf"
	ViewerWeb        = "web"
	SettingMainPath  = "main_path"
	SettingHideNotes = "hide_notes"
	SettingWebOnExit = "web_on_exit"

	// Web Companion behavior after the TUI quits: ask interactively, stop the
	// companion, or keep it running for the browser.
	OnExitAsk  = "ask"
	OnExitStop = "stop"
	OnExitKeep = "keep"
)

// GetViewer returns the configured viewer mode, defaulting to leaf.
func (s *Service) GetViewer(ctx context.Context) (string, error) {
	return s.getSetting(ctx, "viewer")
}

// GetSetting returns the resolved value (stored or spec default) for a key.
func (s *Service) GetSetting(ctx context.Context, key string) (string, error) {
	return s.getSetting(ctx, key)
}

// SetViewer persists the viewer mode after validating it.
func (s *Service) SetViewer(ctx context.Context, viewer string) error {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	return s.SetSetting(ctx, "viewer", viewer)
}

// ListSettings returns every configurable option with its current value.
func (s *Service) ListSettings(ctx context.Context) ([]Setting, error) {
	out := make([]Setting, 0, len(settingSpecs))
	for _, spec := range settingSpecs {
		if spec.Key == SettingMainPath {
			setting, err := s.mainPathSetting(ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, setting)
			continue
		}
		value, err := s.getSetting(ctx, spec.Key)
		if err != nil {
			return nil, err
		}
		out = append(out, Setting{Key: spec.Key, Label: spec.Label, Value: value, Options: append([]string(nil), spec.Options...)})
	}
	return out, nil
}

func (s *Service) mainPathSetting(ctx context.Context) (Setting, error) {
	summaries, err := s.store.ListPaths(ctx, false)
	if err != nil {
		return Setting{}, err
	}
	options := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		options = append(options, summary.Path.Root)
	}
	value := ""
	if len(options) > 0 {
		value = options[0]
	}
	stored, err := s.store.GetSetting(ctx, SettingMainPath)
	if err != nil {
		return Setting{}, err
	}
	stored = strings.TrimSpace(stored)
	if stored != "" {
		for _, option := range options {
			if pathRootsEqual(option, stored) {
				value = option
				break
			}
		}
	}
	return Setting{
		Key:     SettingMainPath,
		Label:   "main path",
		Value:   value,
		Options: options,
	}, nil
}

// SetSetting validates and persists one configurable option.
func (s *Service) SetSetting(ctx context.Context, key, value string) error {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return lockErr
	}
	defer release()
	// agent.* keys are free-form Agent control-plane settings (pi_path,
	// write_tools, max_workers, …). They are not part of the typed TUI spec.
	if strings.HasPrefix(key, "agent.") {
		return s.store.SetSetting(ctx, key, strings.TrimSpace(value))
	}
	spec, ok := findSettingSpec(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	value = strings.TrimSpace(value)
	if key == SettingMainPath {
		summaries, err := s.store.ListPaths(ctx, false)
		if err != nil {
			return err
		}
		for _, summary := range summaries {
			if pathRootsEqual(summary.Path.Root, value) {
				return s.store.SetSetting(ctx, key, summary.Path.Root)
			}
		}
		return fmt.Errorf("main path %q is not a configured path; add it with mm path add", value)
	}
	valid := false
	for _, option := range spec.Options {
		if value == option {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid value %q for %q: use %s", value, key, strings.Join(spec.Options, " or "))
	}
	return s.store.SetSetting(ctx, key, value)
}

func (s *Service) getSetting(ctx context.Context, key string) (string, error) {
	spec, ok := findSettingSpec(key)
	if !ok {
		return "", fmt.Errorf("unknown setting %q", key)
	}
	if key == SettingMainPath {
		setting, err := s.mainPathSetting(ctx)
		if err != nil {
			return "", err
		}
		return setting.Value, nil
	}
	value, err := s.store.GetSetting(ctx, key)
	if err != nil {
		return "", err
	}
	if value == "" {
		return spec.Value, nil
	}
	valid := false
	for _, option := range spec.Options {
		if value == option {
			valid = true
			break
		}
	}
	if !valid {
		return "", fmt.Errorf("invalid value %q for setting %q", value, key)
	}
	return value, nil
}

func findSettingSpec(key string) (Setting, bool) {
	for _, spec := range settingSpecs {
		if spec.Key == key {
			return spec, true
		}
	}
	return Setting{}, false
}

func (s *Service) resolvePath(ctx context.Context, selector string) (*catalog.IndexedPath, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, errors.New("path selector is required")
	}
	if id, err := strconv.ParseInt(selector, 10, 64); err == nil && id > 0 {
		return s.store.ResolvePath(ctx, selector, false)
	}
	canonical, err := s.scanner.Canonicalize(selector)
	if err != nil {
		return nil, err
	}
	return s.store.ResolvePath(ctx, canonical, false)
}
