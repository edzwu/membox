package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"membox/internal/application/port"
)

// PublicationLiveness pairs a publication with its live HTTP status.
type PublicationLiveness struct {
	Publication Publication
	HTTPStatus  int  // 0 when unreachable
	Live        bool // true on HTTP 200
}

// BlogStatusReport is a one-shot snapshot of the deploy pipeline state.
type BlogStatusReport struct {
	Run          port.DeployRun
	RunKnown     bool // false when CI state is unavailable (no gh / no runs)
	Publications []PublicationLiveness
}

// BlogStatus reports the latest CI deploy run and probes every published URL.
func (s *Service) BlogStatus(ctx context.Context) (BlogStatusReport, error) {
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return BlogStatusReport{}, err
	}
	publications, err := s.ListPublications(ctx)
	if err != nil {
		return BlogStatusReport{}, err
	}
	report := BlogStatusReport{}
	if s.deployWatch != nil {
		run, watchErr := s.deployWatch.LatestDeployRun(ctx, blogRoot)
		if watchErr == nil && run.Status != "" {
			report.Run = run
			report.RunKnown = true
		}
	}
	for _, publication := range publications {
		status := probeURL(ctx, publication.URL)
		report.Publications = append(report.Publications, PublicationLiveness{
			Publication: publication,
			HTTPStatus:  status,
			Live:        status == http.StatusOK,
		})
	}
	return report, nil
}

// DeployPhase identifies which stage WatchDeploy is waiting on.
type DeployPhase string

const (
	DeployPhaseCI   DeployPhase = "ci"   // waiting for the GitHub Actions run
	DeployPhaseLive DeployPhase = "live" // waiting for the page to serve HTTP 200
	DeployPhaseDone DeployPhase = "done"
)

// DeployProgress is one tick of deploy watching.
type DeployProgress struct {
	Phase   DeployPhase
	Message string // human-readable state, e.g. "CI in_progress"
	Elapsed time.Duration
}

// WatchDeploy polls until the given publication's page serves HTTP 200,
// reporting progress through the callback. It waits on the CI run first when
// deploy state is available, then on GitHub Pages propagation. The watch
// bound is controlled by ctx; callers should set a deadline.
func (s *Service) WatchDeploy(ctx context.Context, selector string, report func(DeployProgress)) error {
	started := time.Now()
	publication, err := s.GetPublication(ctx, selector)
	if err != nil {
		return err
	}
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return err
	}
	notify := func(phase DeployPhase, message string) {
		if report != nil {
			report(DeployProgress{Phase: phase, Message: message, Elapsed: time.Since(started).Round(time.Second)})
		}
	}

	// Phase 1: CI. Skipped silently when gh or run data is unavailable.
	ciKnown := false
	if s.deployWatch != nil {
		for {
			run, watchErr := s.deployWatch.LatestDeployRun(ctx, blogRoot)
			if watchErr != nil {
				notify(DeployPhaseCI, "CI state unavailable; watching the URL directly")
				break
			}
			ciKnown = true
			switch run.Status {
			case "completed":
				if run.Conclusion != "success" {
					return fmt.Errorf("deploy workflow %s: %s", run.Conclusion, run.URL)
				}
				notify(DeployPhaseCI, "CI succeeded")
				goto live
			default:
				notify(DeployPhaseCI, fmt.Sprintf("CI %s", run.Status))
			}
			if err := sleepCtx(ctx, 5*time.Second); err != nil {
				return err
			}
		}
	}

live:
	// Phase 2: GitHub Pages propagation. The pages repo push happens at the
	// end of CI; Pages itself can take another minute to serve the new URL.
	if !ciKnown {
		notify(DeployPhaseLive, "watching URL (CI state unknown)")
	}
	for {
		status := probeURL(ctx, publication.URL)
		if status == http.StatusOK {
			notify(DeployPhaseDone, fmt.Sprintf("live: %s", publication.URL))
			return nil
		}
		if status == 0 {
			notify(DeployPhaseLive, "URL unreachable")
		} else {
			notify(DeployPhaseLive, fmt.Sprintf("URL returned HTTP %d", status))
		}
		if err := sleepCtx(ctx, 5*time.Second); err != nil {
			return err
		}
	}
}

// probeURL returns the HTTP status for url, or 0 when the request fails.
func probeURL(ctx context.Context, url string) int {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, url, nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.New("deploy watch cancelled or timed out; the site may still be deploying — check: mm blog status")
	case <-timer.C:
		return nil
	}
}
