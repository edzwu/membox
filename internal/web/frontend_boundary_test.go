package web

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func TestMiruSubmoduleMetadataIsNotEmbedded(t *testing.T) {
	_, err := fs.Stat(frontendFS, "frontend/miru/.git")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("frontend/miru/.git must not be embedded: %v", err)
	}
}

func TestMiruFrontendHasNoMemboxBackendDependency(t *testing.T) {
	const root = "frontend/miru"
	forbidden := []string{
		"membox",
		"/api/",
		"../../adapters/companion/",
		"../adapters/companion/",
		`src="/adapters/companion/`,
	}
	if err := fs.WalkDir(frontendFS, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root+"/vendor" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".css") {
			return nil
		}
		body, readErr := fs.ReadFile(frontendFS, path)
		if readErr != nil {
			return readErr
		}
		for _, marker := range forbidden {
			if strings.Contains(string(body), marker) {
				t.Errorf("portable Miru frontend %s contains backend dependency %q", path, marker)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
