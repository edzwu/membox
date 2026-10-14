// Command membox-server is the membox remote node: a 12-factor HTTP server
// that hosts multi-user documents with blobs in Cloudflare R2 and metadata in
// PostgreSQL. It also carries ops subcommands (r2 …) for direct object-store
// work. See docs/membox-remote-stage1-plan.md and docs/remote-stage1.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"membox/internal/infrastructure/postgres"
	"membox/internal/infrastructure/r2"
	"membox/internal/interfaces/httpserver"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if err := newRoot().Execute(); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "membox-server",
		Short: "membox remote node (HTTP server + ops)",
		// Bare `membox-server` starts the HTTP server (T02 behavior).
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd)
		},
	}
	root.AddCommand(newR2Command())
	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Apply pending PostgreSQL migrations (golang-migrate, plan T08)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.DatabaseURL == "" {
				return errors.New("DATABASE_URL not set")
			}
			return postgres.Migrate(cfg.DatabaseURL)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "allow <email>",
		Short: "Add an email to the sign-in allowlist",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.DatabaseURL == "" {
				return errors.New("DATABASE_URL not set")
			}
			pool, err := postgres.Open(cmd.Context(), cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			if err := postgres.AllowEmail(cmd.Context(), pool, args[0]); err != nil {
				return err
			}
			fmt.Printf("allowed %s\n", args[0])
			return nil
		},
	})
	return root
}

// loadConfig applies .env (development convenience; real env wins) and loads
// the validated process configuration (ADR-004).
func loadConfig() (httpserver.Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return httpserver.Config{}, fmt.Errorf("load .env: %w", err)
	}
	return httpserver.Load(os.Getenv)
}

// openR2 builds the R2 blob store from configuration, failing clearly when
// credentials are missing (plan M1 验收: 无凭据时清晰报错).
func openR2(cfg httpserver.Config) (*r2.Store, error) {
	if !cfg.R2Configured() {
		return nil, errors.New("R2 not configured: set R2_ACCOUNT_ID, R2_BUCKET, R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY (see .env.example)")
	}
	return r2.Open(r2.Config{
		AccountID:       cfg.R2AccountID,
		Bucket:          cfg.R2Bucket,
		AccessKeyID:     cfg.R2AccessKeyID,
		SecretAccessKey: cfg.R2SecretAccessKey,
	})
}
