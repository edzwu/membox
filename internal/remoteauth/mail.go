package remoteauth

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Mailer delivers the OTP code. The SMTP implementation is used in
// production; when SMTP_HOST is empty a log mailer keeps local development
// zero-dependency (pair with Mailpit when you want a real inbox, plan T12).
type Mailer interface {
	SendCode(ctx context.Context, email, code string) error
}

type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

type SMTPMailer struct{ cfg SMTPConfig }

func NewSMTPMailer(cfg SMTPConfig) *SMTPMailer { return &SMTPMailer{cfg: cfg} }

func (m *SMTPMailer) SendCode(_ context.Context, email, code string) error {
	if m.cfg.Host == "" {
		return fmt.Errorf("smtp: host not configured")
	}
	addr := net.JoinHostPort(m.cfg.Host, m.cfg.Port)
	body := strings.Join([]string{
		fmt.Sprintf("From: %s", m.cfg.From),
		fmt.Sprintf("To: %s", email),
		"Subject: Your membox sign-in code",
		"",
		fmt.Sprintf("Your membox code is %s — valid for 10 minutes.", code),
		"If you did not request it, ignore this email.",
		"",
	}, "\r\n")
	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}
	if err := smtp.SendMail(addr, auth, m.cfg.From, []string{email}, []byte(body)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// LogMailer prints codes to the server log. Development only — production
// must configure real SMTP (plan T12: never log codes in production).
type LogMailer struct{ warn bool }

func NewLogMailer(production bool) *LogMailer { return &LogMailer{warn: production} }

func (m *LogMailer) SendCode(_ context.Context, email, code string) error {
	if m.warn {
		return fmt.Errorf("smtp not configured: refusing to log OTP codes in production")
	}
	log.Printf("remoteauth: dev OTP for %s: %s", email, code)
	return nil
}

// Limiter is a small fixed-window rate limiter keyed by IP and email
// (plan T11). In-memory is enough for a single-node deployment.
type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	buckets map[string][]time.Time
}

func NewLimiter(window time.Duration, max int) *Limiter {
	return &Limiter{window: window, max: max, buckets: map[string][]time.Time{}}
}

// Allow records one attempt for key and reports whether it is within budget.
func (l *Limiter) Allow(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	for _, key := range keys {
		kept := l.buckets[key][:0]
		for _, at := range l.buckets[key] {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		if len(kept) >= l.max {
			l.buckets[key] = kept
			return false
		}
		l.buckets[key] = append(kept, now)
	}
	return true
}
