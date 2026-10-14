// Package postgres is the remote node's PostgreSQL adapter: connection pool,
// golang-migrate migrations (embedded), and the remotedocs/remoteauth
// repositories. Per ADR-005 it evolves independently from the local SQLite
// schema — they share a conceptual model, not DDL.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects and verifies the pool. Callers own Close.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

// migrateURL rewrites the scheme for golang-migrate's pgx/v5 driver, which
// registers as "pgx" while our DSNs say postgres:// or postgresql://.
func migrateURL(databaseURL string) string {
	for _, scheme := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(databaseURL, scheme) {
			return "pgx5://" + strings.TrimPrefix(databaseURL, scheme)
		}
	}
	return databaseURL
}

// Migrate applies all pending migrations (golang-migrate, plan T08). Run it
// via `membox-server migrate`; the server itself only CHECKS the version.
func Migrate(databaseURL string) error {
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL(databaseURL))
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// CheckVersion fails startup when migrations are pending or the schema is
// dirty — the server never auto-applies migrations (plan T08).
func CheckVersion(databaseURL string) error {
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL(databaseURL))
	if err != nil {
		return err
	}
	defer m.Close()
	version, dirty, err := m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return errors.New("postgres: schema not initialized — run `membox-server migrate`")
		}
		return fmt.Errorf("postgres: schema version: %w", err)
	}
	if dirty {
		return fmt.Errorf("postgres: schema version %d is dirty — fix manually before starting", version)
	}
	return nil
}
