//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package filesystem

import (
	"fmt"
	"os"
	"syscall"

	"membox/internal/domain/catalog"
)

func fileKey(info os.FileInfo) catalog.FileKey {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return catalog.FileKey(fmt.Sprintf("%d:%d", uint64(stat.Dev), uint64(stat.Ino)))
}
