package companion

import (
	"fmt"
	"os"

	"membox/internal/web/backend"
)

// CurrentBinaryFingerprint identifies the running executable by size+mtime so a
// long-lived keep companion can be recycled after a rebuild. It is cheap (no
// hashing of a 20MB+ binary on every Ensure) and stable across identical builds.
func CurrentBinaryFingerprint() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

// binaryCurrent reports whether a running companion was started from the same
// binary that is now on disk. Empty fingerprints (pre-fingerprint bridges) are
// treated as current to avoid needlessly recycling a healthy process once.
func binaryCurrent(home string) bool {
	bridge, err := backend.ReadBridgeFile(home)
	if err != nil {
		return true
	}
	if bridge.HostBinary == "" {
		return true
	}
	return bridge.HostBinary == CurrentBinaryFingerprint()
}
