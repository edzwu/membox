package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"membox/internal/infrastructure/postgres"
	"membox/internal/interfaces/httpserver"
	"membox/internal/remoteauth"
	"membox/internal/remotedocs"
	"membox/internal/remotetopics"
)

// runServe starts the remote node HTTP server (T02). Bare `membox-server`
// lands here.
func runServe(cmd *cobra.Command) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	probes, err := buildProbes(cfg)
	if err != nil {
		return fmt.Errorf("probes: %w", err)
	}

	api := httpserver.New(cfg, probes)

	if cfg.DatabaseURL == "" {
		log.Print("DATABASE_URL not set: auth and document API disabled (healthz/readyz/home still up)")
	} else {
		if err := postgres.CheckVersion(cfg.DatabaseURL); err != nil {
			return err
		}
		pool, err := postgres.Open(cmd.Context(), cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		probes = append(probes, probeFunc{name: "postgres", check: pool.Ping})
		api.EnableAuth(
			mustAuthService(postgres.NewAuthStore(pool), cfg),
			newMailer(cfg),
			remoteauth.NewLimiter(10*time.Minute, 10),
		)
		if cfg.R2Configured() {
			blobs, err := openR2(cfg)
			if err != nil {
				return err
			}
			api.EnableDocs(remotedocs.NewService(
				postgres.NewDocumentStore(pool), blobs,
				postgres.NewDocumentID, cfg.MaxUploadBytes,
			))
		} else {
			log.Print("R2 not configured: document API disabled")
		}
		// Topics/issues need only PostgreSQL — usable even without R2.
		topicStore := postgres.NewTopicStore(pool)
		api.EnableTopics(remotetopics.NewService(topicStore, topicStore, postgres.NewDocumentID))
	}
	api.ReplaceProbes(probes)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("membox-server listening on http://%s (env=%s)", cfg.HTTPAddr, cfg.AppEnv)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		log.Print("membox-server stopped")
		return nil
	case err := <-errCh:
		return err
	}
}

// probeFunc adapts a plain check function to httpserver.Probe.
type probeFunc struct {
	name  string
	check func(context.Context) error
}

func (p probeFunc) Name() string                    { return p.name }
func (p probeFunc) Check(ctx context.Context) error { return p.check(ctx) }

func mustAuthService(store remoteauth.Store, cfg httpserver.Config) *remoteauth.Service {
	service, err := remoteauth.NewService(store, cfg.OTPHMACSecret)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	return service
}

func newMailer(cfg httpserver.Config) remoteauth.Mailer {
	if cfg.SMTPHost != "" {
		return remoteauth.NewSMTPMailer(remoteauth.SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		})
	}
	return remoteauth.NewLogMailer(cfg.AppEnv == "production")
}

// buildProbes registers baseline reachability probes for configured
// dependencies. When the PG pool is available (runServe), a real Ping probe
// replaces the dial-level check via ReplaceProbes.
func buildProbes(cfg httpserver.Config) ([]httpserver.Probe, error) {
	var probes []httpserver.Probe
	if cfg.DatabaseURL != "" {
		probe, err := httpserver.TCPProbe("postgres", cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("DATABASE_URL: %w", err)
		}
		probes = append(probes, probe)
	}
	if cfg.R2Configured() {
		probe, err := httpserver.TCPProbe("r2", cfg.R2Endpoint())
		if err != nil {
			return nil, fmt.Errorf("R2 endpoint: %w", err)
		}
		probes = append(probes, probe)
	}
	return probes, nil
}
