package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/secrets"
	"github.com/phongbui83/maildev/internal/store"
)

const CookieName = "maildev_session"

type Service struct {
	store      *store.Store
	auth       Authenticator
	ttl        time.Duration
	secure     bool
	adminUsers map[string]bool
}

func NewService(st *store.Store, a Authenticator, ttl time.Duration, secure bool, adminUsers []string) *Service {
	m := make(map[string]bool, len(adminUsers))
	for _, u := range adminUsers {
		m[strings.ToLower(u)] = true
	}
	return &Service{store: st, auth: a, ttl: ttl, secure: secure, adminUsers: m}
}

func (s *Service) Authenticator() Authenticator { return s.auth }

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

var ErrAccountLocked = errors.New("account temporarily locked after too many failed logins")

const (
	maxFailedLogins = 5
	lockDuration    = 15 * time.Minute
)

// Login authenticates a local account (password hash) or a directory account,
// upserts the user, applies bootstrap admin roles and pending memberships, and
// returns a new session token.
func (s *Service) Login(ctx context.Context, username, password, ip, ua string) (*model.User, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var u *model.User
	if lu, hash, err := s.store.LocalCredentials(ctx, username); err == nil {
		if lu.Disabled {
			return nil, "", ErrInvalidCredentials
		}
		if lu.LockedUntil != nil && lu.LockedUntil.After(time.Now()) {
			return nil, "", ErrAccountLocked
		}
		if !VerifyPassword(password, hash) {
			if locked, _ := s.store.RecordLoginFailure(ctx, lu.ID, maxFailedLogins, lockDuration); locked {
				return nil, "", ErrAccountLocked
			}
			return nil, "", ErrInvalidCredentials
		}
		if err := s.store.RecordLoginSuccess(ctx, lu.ID); err != nil {
			return nil, "", err
		}
		u = lu
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, "", err
	} else {
		id, err := s.auth.Login(ctx, username, password)
		if err != nil {
			return nil, "", err
		}
		u, err = s.store.UpsertUserFromDirectory(ctx, id.UUID, id.Username, id.DisplayName, id.Email)
		if errors.Is(err, store.ErrConflict) {
			return nil, "", ErrInvalidCredentials
		}
		if err != nil {
			return nil, "", err
		}
	}
	if u.Disabled {
		return nil, "", ErrInvalidCredentials
	}
	if !u.IsLocal() && s.adminUsers[u.Username] && u.Role != model.RoleAdmin {
		if err := s.store.SetUserRole(ctx, u.ID, model.RoleAdmin); err != nil {
			return nil, "", err
		}
		u.Role = model.RoleAdmin
	}
	if err := s.store.MaterializeMembershipsOnLogin(ctx, u); err != nil {
		return nil, "", err
	}
	token := secrets.SessionToken()
	if err := s.store.CreateSession(ctx, hashToken(token), u.ID, time.Now().Add(s.ttl), ip, ua); err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// LoginLocalSession creates a session for an already-authenticated user (used
// after a password change invalidated the previous sessions).
func (s *Service) LoginLocalSession(ctx context.Context, u *model.User, ip, ua string) (*model.User, string, error) {
	token := secrets.SessionToken()
	if err := s.store.CreateSession(ctx, hashToken(token), u.ID, time.Now().Add(s.ttl), ip, ua); err != nil {
		return nil, "", err
	}
	return u, token, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, hashToken(token))
}

func (s *Service) UserFromToken(ctx context.Context, token string) (*model.User, error) {
	if token == "" {
		return nil, store.ErrNotFound
	}
	return s.store.GetSessionUser(ctx, hashToken(token), s.ttl)
}

func (s *Service) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.ttl.Seconds()),
	})
}

func (s *Service) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

type ctxKey int

const userKey ctxKey = 1

func WithUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func UserFrom(ctx context.Context) *model.User {
	u, _ := ctx.Value(userKey).(*model.User)
	return u
}

// Middleware attaches the session user (if any) to the request context.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err == nil && c.Value != "" {
			u, err := s.UserFromToken(r.Context(), c.Value)
			if err == nil {
				r = r.WithContext(WithUser(r.Context(), u))
			} else if !errors.Is(err, store.ErrNotFound) {
				http.Error(w, "session lookup failed", http.StatusInternalServerError)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
