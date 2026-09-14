package application_test

import (
	"context"
	"path/filepath"
	"testing"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func newSettingsService(t *testing.T) *application.Service {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return application.NewService(store, filesystem.NewScanner(), filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{})
}

func TestScanOnStartDefaultsToBackground(t *testing.T) {
	ctx := context.Background()
	service := newSettingsService(t)

	value, err := service.GetSetting(ctx, application.SettingScanOnStart)
	if err != nil {
		t.Fatal(err)
	}
	if value != application.ScanOnStartBackground {
		t.Fatalf("default scan_on_start=%q, want background", value)
	}

	settings, err := service.ListSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, setting := range settings {
		if setting.Key == application.SettingScanOnStart {
			found = true
			if setting.Value != application.ScanOnStartBackground {
				t.Fatalf("listed scan_on_start=%q, want background", setting.Value)
			}
			if len(setting.Options) != 2 || setting.Options[0] != application.ScanOnStartBackground || setting.Options[1] != application.ScanOnStartOff {
				t.Fatalf("unexpected options: %+v", setting.Options)
			}
		}
	}
	if !found {
		t.Fatal("scan_on_start missing from ListSettings")
	}
}

func TestScanOnStartValidationAndPersistence(t *testing.T) {
	ctx := context.Background()
	service := newSettingsService(t)

	if err := service.SetSetting(ctx, application.SettingScanOnStart, application.ScanOnStartOff); err != nil {
		t.Fatalf("off should be accepted: %v", err)
	}
	value, err := service.GetSetting(ctx, application.SettingScanOnStart)
	if err != nil {
		t.Fatal(err)
	}
	if value != application.ScanOnStartOff {
		t.Fatalf("stored scan_on_start=%q, want off", value)
	}

	if err := service.SetSetting(ctx, application.SettingScanOnStart, "always"); err == nil {
		t.Fatal("invalid scan_on_start value should be rejected")
	}
}
