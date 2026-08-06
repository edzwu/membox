//go:build !windows

package companion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetachedCommandSurvivesLauncherContextCancellation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	ctx, cancel := context.WithCancel(context.Background())
	command, err := newDetachedCommand(ctx, "sh", "-c", "sleep 0.1; printf survived > \"$1\"", "sh", marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := command.Wait(); err != nil {
		t.Fatalf("detached child was killed by launcher cancellation: %v", err)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "survived" {
		t.Fatalf("detached child did not finish: body=%q err=%v", body, err)
	}
}
