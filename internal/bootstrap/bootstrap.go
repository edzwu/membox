package bootstrap

import (
	"path/filepath"

	"membox/internal/application"
	"membox/internal/infrastructure/filesystem"
	"membox/internal/infrastructure/git"
	"membox/internal/infrastructure/sqlite"
	"membox/internal/infrastructure/system"
)

func Open(databasePath string) (*application.Service, error) {
	store, err := sqlite.Open(databasePath)
	if err != nil {
		return nil, err
	}
	scanner := filesystem.NewScanner()
	service := application.NewService(
		store,
		scanner,
		filesystem.Reader{},
		filesystem.Writer{},
		system.IDGenerator{},
		system.Clock{},
		git.History{},
	)
	// Every process that opens this home (TUI, CLI, Web Companion) serializes
	// document mutations on one cross-process lock: the DB and the Markdown
	// tree are shared, and WAL alone cannot keep a multi-step resolve → file
	// write → observe → commit sequence atomic across processes.
	lock, lockErr := system.OpenMutationLock(filepath.Dir(databasePath))
	if lockErr != nil {
		_ = store.Close()
		return nil, lockErr
	}
	service.SetMutationLocker(lock)
	return service, nil
}
