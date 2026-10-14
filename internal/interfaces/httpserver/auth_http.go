package httpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"membox/internal/remoteauth"
)

const sessionCookie = "membox_session"

type contextKey string

const userContextKey contextKey = "remoteauth.user"

// authDeps groups what the auth handlers need; nil when the database is not
// configured (development before M2 setup), in which case /v1/auth/* answers
// 503 and authenticated endpoints 401.
type authDeps struct {
	service *remoteauth.Service
	mailer  remoteauth.Mailer
	limiter *remoteauth.Limiter
}

// ---- login flow ---------------------------------------------------------

type otpRequestBody struct {
	Email string `json:"email"`
}

func (s *Server) handleOTPRequest(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	var body otpRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "valid email required")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !s.auth.limiter.Allow("ip:"+ip, "email:"+email) {
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	challengeID, code, err := s.auth.service.RequestCode(r.Context(), email)
	if err != nil {
		// Uniform response for non-allowlisted emails (T11: no membership leak).
		writeJSON(w, http.StatusOK, map[string]string{"status": "if allowed, a code is on its way"})
		return
	}
	if err := s.auth.mailer.SendCode(r.Context(), email, code); err != nil {
		writeError(w, http.StatusBadGateway, "could not deliver code")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "if allowed, a code is on its way",
		"challenge_id": challengeID,
	})
}

type otpVerifyBody struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
}

func (s *Server) handleOTPVerify(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	var body otpVerifyBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	user, err := s.auth.service.VerifyCode(r.Context(), strings.TrimSpace(body.ChallengeID), strings.TrimSpace(body.Code))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "code invalid or expired")
		return
	}
	token, err := s.auth.service.BeginSession(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start session")
		return
	}
	s.setSessionCookie(w, token, remoteauth.SessionTTL)
	writeJSON(w, http.StatusOK, map[string]string{"id": user.ID, "email": user.Email})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			_ = s.auth.service.EndSession(r.Context(), cookie.Value)
		}
	}
	s.setSessionCookie(w, "", -time.Hour) // expire immediately
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := s.userFrom(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": user.ID, "email": user.Email})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}

// ---- middleware ----------------------------------------------------------

// requireAuth resolves the session cookie into a user (once, cached in the
// request context), else 401.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.authenticate(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

func (s *Server) userFrom(r *http.Request) *remoteauth.User {
	if user, ok := r.Context().Value(userContextKey).(*remoteauth.User); ok {
		return user
	}
	return s.authenticate(r)
}

func (s *Server) authenticate(r *http.Request) *remoteauth.User {
	if s.auth == nil {
		return nil
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	user, _, err := s.auth.service.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		return nil
	}
	return &user
}

// csrfGuard rejects cross-site state-changing requests. SameSite=Lax already
// blocks cross-site cookie sends; this adds an Origin allowlist for defense
// in depth (plan T14).
func (s *Server) csrfGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			origin := r.Header.Get("Origin")
			if origin != "" && s.cfg.PublicBaseURL != "" &&
				!strings.HasPrefix(origin, strings.TrimRight(s.cfg.PublicBaseURL, "/")) &&
				!isLoopbackOrigin(origin) {
				writeError(w, http.StatusForbidden, "bad origin")
				return
			}
		}
		next(w, r)
	}
}

func isLoopbackOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://[::1]")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
