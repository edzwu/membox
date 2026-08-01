//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package filesystem

import (
	"os"

	"membox/internal/domain/catalog"
)

func fileKey(_ os.FileInfo) catalog.FileKey { return "" }
