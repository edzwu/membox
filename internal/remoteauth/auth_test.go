package remoteauth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStore implements Store in memory.
type fakeStore struct {
	allowlist  map[string]bool
	challenges map[string]*Challenge
	users      map[string]*User // by email
	sessions   map[string]*Session
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		allowlist:  map[string]bool{"a@x.com": true},
		challenges: map[string]*Challenge{},
		users:      map[string]*User{},
		sessions:   map[string]*Session{},
	}
}

func (f *fakeStore) IsAllowed(_ context.Context, email string) (bool, error) {
	return f.allowlist[email], nil
}

func (f *fakeStore) CreateChallenge(_ context.Context, c Challenge) error {
	f.challenges[c.ID] = &c
	return nil
}

func (f *fakeStore) Challenge(_ context.Context, id string) (Challenge, error) {
	c, ok := f.challenges[id]
	if !ok {
		return Challenge{}, ErrChallengeInvalid
	}
	return *c, nil
}

func (f *fakeStore) BumpAttempts(_ context.Context, id string) (int, error) {
	c := f.challenges[id]
	c.Attempts++
	return c.Attempts, nil
}

func (f *fakeStore) ConsumeChallenge(_ context.Context, id string) (bool, error) {
	c := f.challenges[id]
	if c.Consumed {
		return false, nil
	}
	c.Consumed = true
	return true, nil
}

func (f *fakeStore) FindOrCreateUser(_ context.Context, email string) (User, error) {
	if u, ok := f.users[email]; ok {
		return *u, nil
	}
	u := &User{ID: "u-" + email, Email: email}
	f.users[email] = u
	return *u, nil
}

func (f *fakeStore) CreateSession(_ context.Context, s Session) error {
	f.sessions[s.TokenHash] = &s
	return nil
}

func (f *fakeStore) SessionByTokenHash(_ context.Context, hash string) (Session, User, error) {
	s, ok := f.sessions[hash]
	if !ok {
		return Session{}, User{}, ErrSessionInvalid
	}
	for _, u := range f.users {
		if u.ID == s.UserID {
			return *s, *u, nil
		}
	}
	return Session{}, User{}, ErrSessionInvalid
}

func (f *fakeStore) RevokeSession(_ context.Context, id string) error {
	for _, s := range f.sessions {
		if s.ID == id {
			s.Revoked = true
		}
	}
	return nil
}

const testSecret = "test-secret-at-least-16-chars"

func TestLoginFlowHappyPath(t *testing.T) {
	svc, err := NewService(newFakeStore(), testSecret)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, code, err := svc.RequestCode(ctx, "a@x.com")
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.VerifyCode(ctx, id, code)
	if err != nil {
		t.Fatal(err)
	}
	token, err := svc.BeginSession(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := svc.Authenticate(ctx, token)
	if err != nil || got.Email != "a@x.com" {
		t.Fatalf("authenticate: %v %v", got, err)
	}
	if err := svc.EndSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked session must fail, got %v", err)
	}
}

func TestNotAllowlisted(t *testing.T) {
	svc, _ := NewService(newFakeStore(), testSecret)
	if _, _, err := svc.RequestCode(context.Background(), "evil@x.com"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("want ErrNotAllowed, got %v", err)
	}
}

func TestCodeSingleUse(t *testing.T) {
	svc, _ := NewService(newFakeStore(), testSecret)
	ctx := context.Background()
	id, code, _ := svc.RequestCode(ctx, "a@x.com")
	if _, err := svc.VerifyCode(ctx, id, code); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyCode(ctx, id, code); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("replay must fail, got %v", err)
	}
}

func TestWrongCodeBurnsAttempts(t *testing.T) {
	svc, _ := NewService(newFakeStore(), testSecret)
	ctx := context.Background()
	id, code, _ := svc.RequestCode(ctx, "a@x.com")
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := 0; i < ChallengeTries; i++ {
		_, _ = svc.VerifyCode(ctx, id, wrong)
	}
	if _, err := svc.VerifyCode(ctx, id, code); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("challenge must be locked after %d attempts, got %v", ChallengeTries, err)
	}
}

func TestFirstLoginKeepsUserID(t *testing.T) {
	svc, _ := NewService(newFakeStore(), testSecret)
	ctx := context.Background()
	id1, code1, _ := svc.RequestCode(ctx, "a@x.com")
	u1, _ := svc.VerifyCode(ctx, id1, code1)
	id2, code2, _ := svc.RequestCode(ctx, "a@x.com")
	u2, _ := svc.VerifyCode(ctx, id2, code2)
	if u1.ID != u2.ID {
		t.Fatalf("user ID must be stable across logins: %s vs %s", u1.ID, u2.ID)
	}
}

func TestWeakSecretRejected(t *testing.T) {
	if _, err := NewService(newFakeStore(), "short"); err == nil {
		t.Fatal("short secret must be rejected")
	}
}

func TestLimiter(t *testing.T) {
	limiter := NewLimiter(time.Minute, 3)
	for i := 0; i < 3; i++ {
		if !limiter.Allow("k") {
			t.Fatalf("attempt %d should pass", i)
		}
	}
	if limiter.Allow("k") {
		t.Fatal("4th attempt should be limited")
	}
	if !limiter.Allow("other") {
		t.Fatal("limits are per-key")
	}
}
