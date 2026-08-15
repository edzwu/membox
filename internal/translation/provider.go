package translation

import (
	_ "embed"
	"os"
	"path/filepath"
)

const PiProvider = "membox-ollama"

//go:embed assets/ollama.ts
var providerExtension []byte

// MaterializeProvider writes the build-pinned Pi provider extension under the
// mmd runtime directory. It contains no user data or credentials.
func MaterializeProvider(home string) (string, error) {
	dir := filepath.Join(home, "mmd", "translation")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "ollama.ts")
	tmp, err := os.CreateTemp(dir, "ollama-*.ts.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}
	if _, err := tmp.Write(providerExtension); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Rename(name, target); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return target, nil
}
