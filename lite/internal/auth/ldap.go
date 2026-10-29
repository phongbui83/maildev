// Package auth authenticates web users against Active Directory (LDAPS) or a
// development stub, and manages browser sessions.
package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/config"
)

var ErrInvalidCredentials = errors.New("invalid username or password")
var ErrDirectoryUnavailable = errors.New("directory unavailable")

// Identity is what the directory tells us about a user after a successful login.
type Identity struct {
	UUID        uuid.UUID
	Username    string
	DisplayName string
	Email       string
	DN          string
}

type Authenticator interface {
	// Login verifies the password and returns the directory identity.
	Login(ctx context.Context, username, password string) (*Identity, error)
	// Lookup resolves a username without a password. It returns nil, nil when the
	// backend cannot look users up (no service bind configured).
	Lookup(ctx context.Context, username string) (*Identity, error)
}

type ldapAuth struct {
	cfg config.LDAPConfig
	tls *tls.Config
}

func NewLDAP(cfg config.LDAPConfig) (Authenticator, error) {
	tcfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read LDAP CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("LDAP CA file %s contains no certificates", cfg.CAFile)
		}
		tcfg.RootCAs = pool
	}
	if cfg.InsecureSkipVerify {
		slog.Warn("LDAP TLS certificate verification is DISABLED")
	}
	return &ldapAuth{cfg: cfg, tls: tcfg}, nil
}

func (a *ldapAuth) dial() (*ldap.Conn, error) {
	conn, err := ldap.DialURL(a.cfg.URL, ldap.DialWithTLSConfig(a.tls))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDirectoryUnavailable, err)
	}
	conn.SetTimeout(a.cfg.Timeout)
	return conn, nil
}

var userAttrs = []string{"objectGUID", "sAMAccountName", "displayName", "mail", "userPrincipalName", "cn"}

func (a *ldapAuth) search(conn *ldap.Conn, username string) (*ldap.Entry, error) {
	esc := ldap.EscapeFilter(username)
	filter := a.cfg.UserFilter
	// Replace every %s with the escaped username (filter may reference it more than once).
	filter = strings.ReplaceAll(filter, "%s", esc)
	req := ldap.NewSearchRequest(a.cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, int(a.cfg.Timeout.Seconds()), false, filter, userAttrs, nil)
	res, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("%w: search: %v", ErrDirectoryUnavailable, err)
	}
	if len(res.Entries) == 0 {
		return nil, ErrInvalidCredentials
	}
	if len(res.Entries) > 1 {
		return nil, fmt.Errorf("ambiguous username %q: %d directory entries", username, len(res.Entries))
	}
	return res.Entries[0], nil
}

func entryIdentity(e *ldap.Entry) (*Identity, error) {
	raw := e.GetRawAttributeValue("objectGUID")
	if len(raw) != 16 {
		return nil, fmt.Errorf("entry %s has no objectGUID", e.DN)
	}
	id := Identity{
		UUID:        ADGUIDToUUID(raw),
		Username:    strings.ToLower(e.GetAttributeValue("sAMAccountName")),
		DisplayName: e.GetAttributeValue("displayName"),
		Email:       e.GetAttributeValue("mail"),
		DN:          e.DN,
	}
	if id.Username == "" {
		upn := e.GetAttributeValue("userPrincipalName")
		id.Username = strings.ToLower(strings.SplitN(upn, "@", 2)[0])
	}
	if id.DisplayName == "" {
		id.DisplayName = e.GetAttributeValue("cn")
	}
	return &id, nil
}

// ADGUIDToUUID converts the 16 raw bytes of an AD objectGUID into the UUID that
// AD tools display (the first three fields are little-endian on the wire).
func ADGUIDToUUID(b []byte) uuid.UUID {
	var u uuid.UUID
	u[0], u[1], u[2], u[3] = b[3], b[2], b[1], b[0]
	u[4], u[5] = b[5], b[4]
	u[6], u[7] = b[7], b[6]
	copy(u[8:], b[8:16])
	return u
}

func (a *ldapAuth) Login(ctx context.Context, username, password string) (*Identity, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	// Reject anything that is not a plain account name; the UPN suffix is ours to add.
	if i := strings.Index(username, "@"); i >= 0 {
		username = username[:i]
	}
	if strings.ContainsAny(username, "\\/()*\x00") {
		return nil, ErrInvalidCredentials
	}

	conn, err := a.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if a.cfg.BindDN != "" {
		// Service account: search first, then bind as the user's DN.
		if err := conn.Bind(a.cfg.BindDN, a.cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("%w: service bind: %v", ErrDirectoryUnavailable, err)
		}
		entry, err := a.search(conn, username)
		if err != nil {
			return nil, err
		}
		if err := conn.Bind(entry.DN, password); err != nil {
			if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
				return nil, ErrInvalidCredentials
			}
			return nil, fmt.Errorf("%w: user bind: %v", ErrDirectoryUnavailable, err)
		}
		return entryIdentity(entry)
	}

	// No service account: bind as UPN, then read our own entry.
	upn := username + "@" + a.cfg.UPNSuffix
	if err := conn.Bind(upn, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("%w: bind: %v", ErrDirectoryUnavailable, err)
	}
	entry, err := a.search(conn, username)
	if err != nil {
		return nil, err
	}
	return entryIdentity(entry)
}

func (a *ldapAuth) Lookup(ctx context.Context, username string) (*Identity, error) {
	if a.cfg.BindDN == "" {
		return nil, nil
	}
	conn, err := a.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.Bind(a.cfg.BindDN, a.cfg.BindPassword); err != nil {
		return nil, fmt.Errorf("%w: service bind: %v", ErrDirectoryUnavailable, err)
	}
	entry, err := a.search(conn, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, nil
		}
		return nil, err
	}
	return entryIdentity(entry)
}

// devAuth accepts any username with the shared development password. It exists so
// the stack can be run locally without Active Directory and must never be enabled
// in production.
type devAuth struct {
	password string
}

var devNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

func NewDev() Authenticator {
	pw := os.Getenv("MAILDEV_DEV_PASSWORD")
	if pw == "" {
		pw = "dev"
	}
	slog.Warn("AUTH MODE IS 'dev': any username logs in with the shared dev password. Do not expose this instance.")
	return &devAuth{password: pw}
}

func (d *devAuth) Login(ctx context.Context, username, password string) (*Identity, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" || password != d.password {
		return nil, ErrInvalidCredentials
	}
	id, _ := d.Lookup(ctx, username)
	return id, nil
}

func (d *devAuth) Lookup(ctx context.Context, username string) (*Identity, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	return &Identity{
		UUID:        uuid.NewSHA1(devNamespace, []byte("maildev-dev:"+username)),
		Username:    username,
		DisplayName: strings.ToUpper(username[:1]) + username[1:],
		Email:       username + "@example.test",
		DN:          "dev",
	}, nil
}

// unused guard to keep time imported for timeouts in future tweaks
var _ = time.Second
