// Package remoteauth implements the remote node's email OTP login: allowlist
// check, HMAC-stored one-time challenges, hashed session tokens, and request
// rate limiting (plan M3). It never stores plaintext codes or tokens.
package remoteauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors surface as generic 401/400 responses so the API never
// leaks allowlist membership or challenge state (plan T11).
var (
	ErrNotAllowed       = errors.New("email not on allowlist")
	ErrChallengeInvalid = errors.New("code invalid or expired")
	ErrSessionInvalid   = errors.New("session invalid or expired")
	ErrRateLimited      = errors.New("too many requests, slow down")
)

const (
	ChallengeTTL   = 10 * time.Minute
	ChallengeTries = 5
	SessionTTL     = 30 * 24 * time.Hour
)

type User struct {
	ID    string
	Email string
}

type Challenge struct {
	ID        string
	Email     string
	CodeHMAC  string
	Attempts  int
	ExpiresAt time.Time
	Consumed  bool
}

type Session struct {
	ID        string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	Revoked   bool
}

// Store is the persistence contract implemented by PostgreSQL (T10).
type Store interface {
	IsAllowed(ctx context.Context, email string) (bool, error)
	CreateChallenge(ctx context.Context, c Challenge) error
	Challenge(ctx context.Context, id string) (Challenge, error)
	// BumpAttempts returns the new attempt count.
	BumpAttempts(ctx context.Context, id string) (int, error)
	// ConsumeChallenge atomically marks the challenge consumed; it reports
	// false when the row was already consumed (replay defense, T13).
	ConsumeChallenge(ctx context.Context, id string) (bool, error)
	FindOrCreateUser(ctx context.Context, email string) (User, error)
	CreateSession(ctx context.Context, s Session) error
	// SessionByTokenHash returns the session joined with its user.
	SessionByTokenHash(ctx context.Context, hash string) (Session, User, error)
	RevokeSession(ctx context.Context, id string) error
}

// Service issues and verifies OTP challenges and sessions.
type Service struct {
	store  Store
	secret []byte // OTP_HMAC_SECRET
	now    func() time.Time
}

func NewService(store Store, hmacSecret string) (*Service, error) {
	if len(hmacSecret) < 16 {
		return nil, errors.New("OTP_HMAC_SECRET must be at least 16 characters")
	}
	return &Service{store: store, secret: []byte(hmacSecret), now: time.Now}, nil
}

func (s *Service) codeHMAC(code string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// newID mints UUIDv7 row IDs (challenge/session PKs are UUID columns); the
// OTP code and session token are separate high-entropy secrets.
func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func randomDigits(n int) (string, error) {
	digits := make([]byte, n)
	for i := range digits {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		digits[i] = byte('0' + v.Int64())
	}
	return string(digits), nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RequestCode validates the allowlist, issues a 6-digit challenge, and
// returns the plaintext code exactly once (the caller emails it; only its
// HMAC is stored).
func (s *Service) RequestCode(ctx context.Context, email string) (challengeID, code string, err error) {
	allowed, err := s.store.IsAllowed(ctx, email)
	if err != nil {
		return "", "", err
	}
	if !allowed {
		return "", "", ErrNotAllowed
	}
	code, err = randomDigits(6)
	if err != nil {
		return "", "", err
	}
	id, err := newID()
	if err != nil {
		return "", "", err
	}
	c := Challenge{
		ID:        id,
		Email:     email,
		CodeHMAC:  s.codeHMAC(code),
		ExpiresAt: s.now().Add(ChallengeTTL),
	}
	if err := s.store.CreateChallenge(ctx, c); err != nil {
		return "", "", err
	}
	return id, code, nil
}

// VerifyCode checks a code and, on success, atomically consumes the
// challenge and returns the user (created on first login, T13).
func (s *Service) VerifyCode(ctx context.Context, challengeID, code string) (User, error) {
	challenge, err := s.store.Challenge(ctx, challengeID)
	if err != nil {
		return User{}, err
	}
	if challenge.Consumed || s.now().After(challenge.ExpiresAt) || challenge.Attempts >= ChallengeTries {
		return User{}, ErrChallengeInvalid
	}
	attempts, err := s.store.BumpAttempts(ctx, challengeID)
	if err != nil {
		return User{}, err
	}
	if attempts > ChallengeTries {
		return User{}, ErrChallengeInvalid
	}
	want := s.codeHMAC(code)
	if subtle.ConstantTimeCompare([]byte(want), []byte(challenge.CodeHMAC)) != 1 {
		return User{}, ErrChallengeInvalid
	}
	consumed, err := s.store.ConsumeChallenge(ctx, challengeID)
	if err != nil {
		return User{}, err
	}
	if !consumed {
		return User{}, ErrChallengeInvalid // replayed
	}
	return s.store.FindOrCreateUser(ctx, challenge.Email)
}

// BeginSession issues a new session token; only its SHA-256 hash is stored.
func (s *Service) BeginSession(ctx context.Context, userID string) (token string, err error) {
	token, err = randomToken()
	if err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	err = s.store.CreateSession(ctx, Session{
		ID:        id,
		UserID:    userID,
		TokenHash: hashToken(token),
		ExpiresAt: s.now().Add(SessionTTL),
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Authenticate resolves a session token to its user.
func (s *Service) Authenticate(ctx context.Context, token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrSessionInvalid
	}
	session, user, err := s.store.SessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		return User{}, Session{}, err
	}
	if session.Revoked || s.now().After(session.ExpiresAt) {
		return User{}, Session{}, ErrSessionInvalid
	}
	return user, session, nil
}

// EndSession revokes the session behind a token (logout is idempotent).
func (s *Service) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	session, _, err := s.store.SessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		return nil // unknown token: nothing to revoke
	}
	return s.store.RevokeSession(ctx, session.ID)
}

// DebugString redacts secrets in logs.
func (c Challenge) DebugString() string {
	return fmt.Sprintf("challenge %s for %s (attempts %d)", c.ID, c.Email, c.Attempts)
}
