package httpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"membox/internal/remoteauth"
	"membox/internal/remotedocs"
	"membox/internal/remotetopics"
)

// Probe checks one external dependency. Implementations must be cheap; the
// server additionally caches results for Config.ReadinessCacheTTL so frequent
// /readyz polling never turns into a dependency DDoS.
type Probe interface {
	Name() string
	Check(ctx context.Context) error
}

// tcpProbe is the M0 placeholder readiness check: it dials the dependency's
// host:port. T05 (R2) and T07 (PostgreSQL) replace it with real client pings
// without changing the /readyz contract.
type tcpProbe struct {
	name    string
	address string // host:port
}

func (p tcpProbe) Name() string { return p.name }

func (p tcpProbe) Check(ctx context.Context) error {
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return err
	}
	return conn.Close()
}

// TCPProbe builds a dial-based probe for a URL or DSN (postgres://…, https://…).
func TCPProbe(name, rawURL string) (Probe, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, &net.ParseError{Text: rawURL}
	}
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			port = "5432" // postgres:// and postgresql://
		}
	}
	return tcpProbe{name: name, address: net.JoinHostPort(host, port)}, nil
}

type cachedProbe struct {
	probe   Probe
	ttl     time.Duration
	mu      sync.Mutex
	expires time.Time
	err     error
}

func (c *cachedProbe) check(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c.err = c.probe.Check(probeCtx)
	c.expires = time.Now().Add(c.ttl)
	return c.err
}

// Server is the remote node HTTP API. Dependencies arrive as they are
// configured; handlers must treat every dependency as potentially absent.
type Server struct {
	cfg     Config
	probes  []*cachedProbe
	mux     *http.ServeMux
	started time.Time
	auth    *authDeps
	docs    *remotedocs.Service
	topics  *remotetopics.Service
}

// EnableAuth turns on the OTP login endpoints (M3).
func (s *Server) EnableAuth(service *remoteauth.Service, mailer remoteauth.Mailer, limiter *remoteauth.Limiter) {
	s.auth = &authDeps{service: service, mailer: mailer, limiter: limiter}
}

// EnableDocs turns on the document API (M4).
func (s *Server) EnableDocs(docs *remotedocs.Service) {
	s.docs = docs
}

// EnableTopics turns on the topics/issues API.
func (s *Server) EnableTopics(topics *remotetopics.Service) {
	s.topics = topics
}

// New builds the API server. probes may be empty in M0; /readyz then reports
// only which dependencies are unconfigured.
func New(cfg Config, probes []Probe) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux(), started: time.Now()}
	for _, probe := range probes {
		s.probes = append(s.probes, &cachedProbe{probe: probe, ttl: cfg.ReadinessCacheTTL})
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.registerAuth()
	s.registerDocuments()
	s.registerTopics()
	s.registerHome()
	return s
}

// ReplaceProbes swaps the readiness probe set (runServe upgrades dial-level
// probes to real client checks once the pool exists).
func (s *Server) ReplaceProbes(probes []Probe) {
	s.probes = nil
	for _, probe := range probes {
		s.probes = append(s.probes, &cachedProbe{probe: probe, ttl: s.cfg.ReadinessCacheTTL})
	}
}

// Handler exposes the root handler for http.Server and tests.
func (s *Server) Handler() http.Handler { return s.mux }

// Config returns the loaded configuration (read-only).
func (s *Server) Config() Config { return s.cfg }

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"env":     s.cfg.AppEnv,
		"uptimeS": int64(time.Since(s.started).Seconds()),
	})
}

type readinessReport struct {
	Status       string            `json:"status"` // ready | not_ready
	Dependencies map[string]string `json:"dependencies"`
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	report := readinessReport{Status: "ready", Dependencies: map[string]string{}}
	for name, configured := range s.configuredDependencies() {
		if !configured {
			report.Dependencies[name] = "unconfigured"
		}
	}
	status := http.StatusOK
	for _, probe := range s.probes {
		if err := probe.check(r.Context()); err != nil {
			report.Dependencies[probe.probe.Name()] = "unavailable: " + oneLine(err.Error())
			status = http.StatusServiceUnavailable
		} else {
			report.Dependencies[probe.probe.Name()] = "ok"
		}
	}
	if status != http.StatusOK {
		report.Status = "not_ready"
	}
	writeJSON(w, status, report)
}

// configuredDependencies lists every infrastructure dependency the current
// milestone knows about. Unconfigured entries are reported as information
// only — they never fail readiness. Once a dependency becomes mandatory
// (e.g. PostgreSQL in M2), config validation rejects startup instead.
func (s *Server) configuredDependencies() map[string]bool {
	return map[string]bool{
		"postgres": s.cfg.DatabaseURL != "",
		"r2":       s.cfg.R2Configured(),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
}
