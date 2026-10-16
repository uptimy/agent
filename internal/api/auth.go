package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/uptimy/agent/internal/store"
)

const (
	sessionCookie   = "uptimy_agent_session"
	sessionLifetime = 30 * 24 * time.Hour
	minPasswordLen  = 8
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{2,64}$`)

// dummyHash makes a login for an unknown username cost the same as a wrong
// password, so response timing doesn't reveal which usernames exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("uptimy-agent-dummy"), bcrypt.DefaultCost)

func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

// RandomPassword returns a readable random password.
func RandomPassword() string {
	buf := make([]byte, 12)
	rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf) // 16 chars
}

// EnsureDefaultAdmin makes sure there is someone who can sign in.
//
// On first start it creates the default admin (username, usually "admin").
// Its password is envPassword (ADMIN_PASSWORD) when set; otherwise a random
// one is generated, returned for the caller to log once, and must be changed
// at first sign-in. There is deliberately no well-known default password:
// agents often run on public URLs.
//
// When envPassword is set on later starts, that user's password is reset to
// it and the user is made an admin. That keeps env-driven deployments in sync
// and doubles as the recovery path for a lost password.
func EnsureDefaultAdmin(ctx context.Context, st *store.Store, username, envPassword string) (generated string, err error) {
	if !usernamePattern.MatchString(username) {
		return "", fmt.Errorf("ADMIN_USERNAME %q is not a valid username", username)
	}
	if envPassword != "" && len(envPassword) < minPasswordLen {
		return "", errors.New("ADMIN_PASSWORD must be at least 8 characters")
	}
	count, err := st.CountUsers(ctx)
	if err != nil {
		return "", err
	}

	if count == 0 {
		password, mustChange := envPassword, false
		if password == "" {
			generated = RandomPassword()
			password, mustChange = generated, true
		}
		hash, err := hashPassword(password)
		if err != nil {
			return "", err
		}
		_, err = st.CreateUser(ctx, store.User{Username: username, PasswordHash: hash, Role: store.RoleAdmin, MustChangePassword: mustChange})
		return generated, err
	}

	if envPassword == "" {
		return "", nil
	}
	u, err := st.GetUserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		hash, err := hashPassword(envPassword)
		if err != nil {
			return "", err
		}
		_, err = st.CreateUser(ctx, store.User{Username: username, PasswordHash: hash, Role: store.RoleAdmin})
		return "", err
	}
	if err != nil {
		return "", err
	}
	if !u.IsAdmin() {
		if err := st.SetUserRole(ctx, u.ID, store.RoleAdmin); err != nil {
			return "", err
		}
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(envPassword)) != nil || u.MustChangePassword {
		hash, err := hashPassword(envPassword)
		if err != nil {
			return "", err
		}
		if err := st.SetUserPassword(ctx, u.ID, hash, false); err != nil {
			return "", err
		}
		return "", st.DeleteUserSessions(ctx, u.ID, "")
	}
	return "", nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type userKey struct{}

func currentUser(r *http.Request) store.User {
	u, _ := r.Context().Value(userKey{}).(store.User)
	return u
}

func (s *Server) sessionUser(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	u, err := s.Store.SessionUser(r.Context(), hashToken(c.Value))
	return u, err == nil
}

// passwordManagedByEnv reports whether u's password comes from ADMIN_PASSWORD
// (and would be reset to it on the next start).
func (s *Server) passwordManagedByEnv(u store.User) bool {
	return s.Config.AdminPassword != "" && strings.EqualFold(u.Username, s.Config.AdminUsername)
}

// requireAuth admits signed-in users and API tokens (see tokens.go).
// Viewers are read-only, and a user who must change their password can do
// nothing else until they have.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readOnly := r.Method == http.MethodGet || r.Method == http.MethodHead
		if bearer, ok := bearerToken(r); ok {
			s.serveWithToken(w, r, bearer, readOnly, next)
			return
		}
		u, ok := s.sessionUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !readOnly && !preflighted(r) {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		// Everyone, viewers included, manages their own account under /api/auth/.
		ownAccount := strings.HasPrefix(r.URL.Path, "/api/auth/")
		if u.MustChangePassword && r.URL.Path != "/api/auth/password" {
			writeError(w, http.StatusForbidden, "password change required")
			return
		}
		if !readOnly && !ownAccount && !u.IsAdmin() {
			writeError(w, http.StatusForbidden, "your account has read-only access")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}

// requireAdmin admits admins only, for reads that viewers shouldn't see.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).IsAdmin() {
			writeError(w, http.StatusForbidden, "admins only")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

type meResponse struct {
	store.User
	PasswordManagedByEnv bool `json:"password_managed_by_env"`
}

func (s *Server) me(u store.User) meResponse {
	return meResponse{User: u, PasswordManagedByEnv: s.passwordManagedByEnv(u)}
}

func (s *Server) authState(w http.ResponseWriter, r *http.Request) {
	u, ok := s.sessionUser(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "user": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": s.me(u)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	ip := s.clientIP(r)
	if !s.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		// Code is the second factor, for accounts that have it on: an
		// authenticator code or a recovery code.
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := s.Store.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	hash := dummyHash
	if err == nil {
		hash = []byte(u.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(req.Password)) != nil || err != nil {
		s.limiter.fail(ip)
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	if u.TwoFactor {
		if strings.TrimSpace(req.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "enter the code from your authenticator app", "two_factor_required": true})
			return
		}
		ok, err := s.checkSecondFactor(r.Context(), u, req.Code)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if !ok {
			s.limiter.fail(ip)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "that code didn't work; use the newest one, or a recovery code", "two_factor_required": true})
			return
		}
	}
	if err := s.Store.TouchLogin(r.Context(), u.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	now := time.Now().UTC()
	u.LastLoginAt = &now
	s.startSession(w, r, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User) {
	buf := make([]byte, 32)
	rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)
	expires := time.Now().Add(sessionLifetime)
	if err := s.Store.CreateSession(r.Context(), hashToken(token), u.ID, expires); err != nil {
		s.internalError(w, r, err)
		return
	}
	c := s.sessionCookieFor(r, token) //nolint:gosec // G124: Secure whenever served over HTTPS; see sessionCookieFor
	c.Expires = expires
	http.SetCookie(w, c)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": s.me(u)})
}

// sessionCookieFor builds the session cookie. It's Secure whenever the agent
// is reached over HTTPS (directly or behind a trusted proxy); plain-HTTP
// installs on localhost or a private network must still be able to sign in.
func (s *Server) sessionCookieFor(r *http.Request, value string) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure whenever the request came over HTTPS (see above)
		Name: sessionCookie, Value: value, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.isHTTPS(r),
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			s.Log.Warn("deleting session on sign-out", "err", err)
		}
	}
	c := s.sessionCookieFor(r, "") //nolint:gosec // G124: see sessionCookieFor
	c.MaxAge = -1
	http.SetCookie(w, c)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// changePassword changes the signed-in user's own password and signs out
// their other sessions.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if s.passwordManagedByEnv(u) {
		writeError(w, http.StatusConflict, "this password is set by the ADMIN_PASSWORD environment variable; change it there")
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Current)) != nil {
		writeError(w, http.StatusBadRequest, "current password is wrong")
		return
	}
	if len(req.New) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if req.New == req.Current {
		writeError(w, http.StatusBadRequest, "choose a password you haven't used here")
		return
	}
	hash, err := hashPassword(req.New)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), u.ID, hash, false); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.Store.DeleteUserSessions(r.Context(), u.ID, s.currentSessionHash(r)); err != nil {
		s.internalError(w, r, err)
		return
	}
	u.MustChangePassword = false
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": s.me(u)})
}

func (s *Server) currentSessionHash(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return hashToken(c.Value)
	}
	return ""
}

// sessions reports how many devices the user is signed in on.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUserSessions(r.Context(), currentUser(r).ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

// signOutOthers ends every session of the user except this one.
func (s *Server) signOutOthers(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if err := s.Store.DeleteUserSessions(r.Context(), u.ID, s.currentSessionHash(r)); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": 1})
}

// loginLimiter allows a handful of failed logins per IP per window.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
}

const (
	loginWindow      = 5 * time.Minute
	loginMaxFailures = 10
)

func newLoginLimiter() *loginLimiter { return &loginLimiter{failures: map[string][]time.Time{}} }

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-loginWindow)
	recent := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.failures, ip)
	} else {
		l.failures[ip] = recent
	}
	return len(recent) < loginMaxFailures
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	l.failures[ip] = append(l.failures[ip], time.Now())
	l.mu.Unlock()
}
