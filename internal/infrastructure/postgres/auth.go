package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"membox/internal/remoteauth"
)

// AllowEmail adds an address to the sign-in allowlist (ops command).
func AllowEmail(ctx context.Context, pool *pgxpool.Pool, email string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO auth_allowlist(email) VALUES ($1)
		ON CONFLICT (email) DO NOTHING`, email)
	return err
}

// AuthStore implements remoteauth.Store.
type AuthStore struct {
	pool *pgxpool.Pool
}

func NewAuthStore(pool *pgxpool.Pool) *AuthStore { return &AuthStore{pool: pool} }

func (s *AuthStore) IsAllowed(ctx context.Context, email string) (bool, error) {
	var one int
	err := s.pool.QueryRow(ctx, `SELECT 1 FROM auth_allowlist WHERE email = $1`, email).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *AuthStore) CreateChallenge(ctx context.Context, c remoteauth.Challenge) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO otp_challenges(id, email, code_hmac, expires_at)
		VALUES ($1,$2,$3,$4)`, c.ID, c.Email, c.CodeHMAC, c.ExpiresAt)
	return err
}

func (s *AuthStore) Challenge(ctx context.Context, id string) (remoteauth.Challenge, error) {
	var c remoteauth.Challenge
	var consumedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, code_hmac, attempts, expires_at, consumed_at
		FROM otp_challenges WHERE id = $1`, id,
	).Scan(&c.ID, &c.Email, &c.CodeHMAC, &c.Attempts, &c.ExpiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return remoteauth.Challenge{}, remoteauth.ErrChallengeInvalid
	}
	c.Consumed = consumedAt != nil
	return c, err
}

func (s *AuthStore) BumpAttempts(ctx context.Context, id string) (int, error) {
	var attempts int
	err := s.pool.QueryRow(ctx, `
		UPDATE otp_challenges SET attempts = attempts + 1
		WHERE id = $1 RETURNING attempts`, id).Scan(&attempts)
	return attempts, err
}

func (s *AuthStore) ConsumeChallenge(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE otp_challenges SET consumed_at = now()
		WHERE id = $1 AND consumed_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *AuthStore) FindOrCreateUser(ctx context.Context, email string) (remoteauth.User, error) {
	var user remoteauth.User
	err := s.pool.QueryRow(ctx, `SELECT id, email FROM users WHERE email = $1`, email).
		Scan(&user.ID, &user.Email)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return remoteauth.User{}, err
	}
	user = remoteauth.User{ID: newUUID(), Email: email}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO users(id, email) VALUES ($1,$2)
		ON CONFLICT (email) DO NOTHING`, user.ID, user.Email)
	if err != nil {
		return remoteauth.User{}, err
	}
	// A concurrent first login may have won the insert; re-read for the
	// canonical ID (T13: 后续沿用原用户 UUID).
	err = s.pool.QueryRow(ctx, `SELECT id, email FROM users WHERE email = $1`, email).
		Scan(&user.ID, &user.Email)
	return user, err
}

func (s *AuthStore) CreateSession(ctx context.Context, session remoteauth.Session) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions(id, user_id, token_hash, expires_at)
		VALUES ($1,$2,$3,$4)`, session.ID, session.UserID, session.TokenHash, session.ExpiresAt)
	return err
}

func (s *AuthStore) SessionByTokenHash(ctx context.Context, hash string) (remoteauth.Session, remoteauth.User, error) {
	var session remoteauth.Session
	var user remoteauth.User
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.expires_at, s.revoked_at, u.id, u.email
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1`, hash,
	).Scan(&session.ID, &session.UserID, &session.ExpiresAt, &revokedAt, &user.ID, &user.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return remoteauth.Session{}, remoteauth.User{}, remoteauth.ErrSessionInvalid
	}
	session.TokenHash = hash
	session.Revoked = revokedAt != nil
	return session, user, err
}

func (s *AuthStore) RevokeSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1`, id)
	return err
}
