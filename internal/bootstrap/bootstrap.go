package bootstrap

import (
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
	return application.NewService(store, scanner, filesystem.Reader{}, filesystem.Writer{}, system.IDGenerator{}, system.Clock{}, git.History{}), nil
}
