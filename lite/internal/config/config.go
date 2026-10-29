// Package config loads runtime configuration from environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr string

	// SMTP listeners. SMTPAddrs accept plaintext and STARTTLS; SMTPSAddrs are implicit TLS.
	SMTPAddrs  []string
	SMTPSAddrs []string
	// IMAP listeners. IMAPAddrs accept plaintext and STARTTLS; IMAPSAddrs are implicit TLS.
	IMAPAddrs  []string
	IMAPSAddrs []string
	// POP3 listeners. POP3Addrs accept plaintext and STLS; POP3SAddrs are implicit TLS.
	POP3Addrs  []string
	POP3SAddrs []string

	// Public ports shown to users (when a load balancer maps 25/465/587/143/993 onto the container ports).
	PublicSMTPPorts  []string
	PublicSMTPSPorts []string
	PublicIMAPPorts  []string
	PublicIMAPSPorts []string
	PublicPOP3Ports  []string
	PublicPOP3SPorts []string

	Hostname  string // advertised in SMTP/IMAP greetings and used for the self-signed cert
	PublicURL string // e.g. https://maildev.example.com (web UI)
	MailHost  string // e.g. mail.maildev.example.com (SMTP/IMAP endpoint shown to users)

	TLSCertFile string
	TLSKeyFile  string

	DatabaseURL string
	MasterKey   []byte // 32 bytes, encrypts stored credentials

	AuthMode string // "ldap", "local" (password accounts only) or "dev"
	Edition  string // "full" (multi-project, directory) or "lite" (single project, local accounts)
	LDAP     LDAPConfig

	AdminUsers []string // usernames granted the admin role on login

	SessionTTL      time.Duration
	CookieSecure    bool
	MaxMessageBytes int64

	DemoProject   bool
	LogMailEvents bool

	// OpenBao Transit envelope encryption of stored mail (enabled when Addr is set).
	OpenBaoAddr         string
	OpenBaoToken        string
	OpenBaoRoleID       string
	OpenBaoSecretID     string
	OpenBaoTransitMount string
	EncryptBacklog      bool // encrypt messages stored before encryption was enabled

	// Company user directory (JSON list) used for the member picker.
	DirectoryURL            string
	DirectoryDisableMissing bool // disable AD accounts that vanished from the list

	// S/MIME signature verification trust sources.
	SMIMECAURLs      []string // e.g. https://vault.example.com/v1/pki-int/ca_chain
	SMIMECAFile      string
	SMIMESystemRoots bool
}

type LDAPConfig struct {
	URL                string
	BaseDN             string
	UPNSuffix          string
	BindDN             string
	BindPassword       string
	UserFilter         string
	CAFile             string
	InsecureSkipVerify bool
	Timeout            time.Duration
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envList(key, def string) []string {
	raw := env(key, def)
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(env(key, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func Load() (*Config, error) {
	c := &Config{
		HTTPAddr:         env("MAILDEV_HTTP_ADDR", ":8080"),
		SMTPAddrs:        envList("MAILDEV_SMTP_ADDRS", ":1025"),
		SMTPSAddrs:       envList("MAILDEV_SMTPS_ADDRS", ""),
		IMAPAddrs:        envList("MAILDEV_IMAP_ADDRS", ":1143"),
		IMAPSAddrs:       envList("MAILDEV_IMAPS_ADDRS", ""),
		POP3Addrs:        envList("MAILDEV_POP3_ADDRS", ":1110"),
		POP3SAddrs:       envList("MAILDEV_POP3S_ADDRS", ""),
		PublicSMTPPorts:  envList("MAILDEV_PUBLIC_SMTP_PORTS", ""),
		PublicSMTPSPorts: envList("MAILDEV_PUBLIC_SMTPS_PORTS", ""),
		PublicIMAPPorts:  envList("MAILDEV_PUBLIC_IMAP_PORTS", ""),
		PublicIMAPSPorts: envList("MAILDEV_PUBLIC_IMAPS_PORTS", ""),
		PublicPOP3Ports:  envList("MAILDEV_PUBLIC_POP3_PORTS", ""),
		PublicPOP3SPorts: envList("MAILDEV_PUBLIC_POP3S_PORTS", ""),
		Hostname:         env("MAILDEV_HOSTNAME", "maildev.local"),
		PublicURL:        strings.TrimRight(env("MAILDEV_PUBLIC_URL", "http://localhost:3000"), "/"),
		MailHost:         env("MAILDEV_MAIL_HOST", ""),

		TLSCertFile: env("MAILDEV_TLS_CERT", ""),
		TLSKeyFile:  env("MAILDEV_TLS_KEY", ""),

		DatabaseURL: env("MAILDEV_DATABASE_URL", "postgres://maildev:maildev@localhost:5432/maildev?sslmode=disable"),

		AuthMode: strings.ToLower(env("MAILDEV_AUTH_MODE", "ldap")),
		Edition:  strings.ToLower(env("MAILDEV_EDITION", "full")),
		LDAP: LDAPConfig{
			URL:                env("MAILDEV_LDAP_URL", "ldaps://dc01.example.com:636"),
			BaseDN:             env("MAILDEV_LDAP_BASE_DN", "ou=members,dc=example,dc=com"),
			UPNSuffix:          env("MAILDEV_LDAP_UPN_SUFFIX", "example.com"),
			BindDN:             env("MAILDEV_LDAP_BIND_DN", ""),
			BindPassword:       env("MAILDEV_LDAP_BIND_PASSWORD", ""),
			UserFilter:         env("MAILDEV_LDAP_USER_FILTER", "(&(objectClass=user)(|(sAMAccountName=%s)(userPrincipalName=%s)))"),
			CAFile:             env("MAILDEV_LDAP_CA_FILE", ""),
			InsecureSkipVerify: envBool("MAILDEV_LDAP_INSECURE_SKIP_VERIFY", false),
		},
		AdminUsers:              envList("MAILDEV_ADMIN_USERS", ""),
		CookieSecure:            envBool("MAILDEV_COOKIE_SECURE", true),
		DemoProject:             envBool("MAILDEV_DEMO_PROJECT", true),
		OpenBaoAddr:             env("MAILDEV_OPENBAO_ADDR", ""),
		OpenBaoToken:            env("MAILDEV_OPENBAO_TOKEN", ""),
		OpenBaoRoleID:           env("MAILDEV_OPENBAO_ROLE_ID", ""),
		OpenBaoSecretID:         env("MAILDEV_OPENBAO_SECRET_ID", ""),
		OpenBaoTransitMount:     env("MAILDEV_OPENBAO_TRANSIT_MOUNT", "transit"),
		EncryptBacklog:          envBool("MAILDEV_ENCRYPT_BACKLOG", true),
		DirectoryURL:            env("MAILDEV_DIRECTORY_URL", ""),
		DirectoryDisableMissing: envBool("MAILDEV_DIRECTORY_DISABLE_MISSING", false),
		SMIMECAURLs:             envList("MAILDEV_SMIME_CA_URLS", ""),
		SMIMECAFile:             env("MAILDEV_SMIME_CA_FILE", ""),
		SMIMESystemRoots:        envBool("MAILDEV_SMIME_SYSTEM_ROOTS", true),
		LogMailEvents:           envBool("MAILDEV_LOG_MAIL_EVENTS", true),
	}
	if c.MailHost == "" {
		c.MailHost = c.Hostname
	}
	for i, u := range c.AdminUsers {
		c.AdminUsers[i] = strings.ToLower(u)
	}

	ttl, err := time.ParseDuration(env("MAILDEV_SESSION_TTL", "12h"))
	if err != nil {
		return nil, fmt.Errorf("MAILDEV_SESSION_TTL: %w", err)
	}
	c.SessionTTL = ttl

	lt, err := time.ParseDuration(env("MAILDEV_LDAP_TIMEOUT", "10s"))
	if err != nil {
		return nil, fmt.Errorf("MAILDEV_LDAP_TIMEOUT: %w", err)
	}
	c.LDAP.Timeout = lt

	mb, err := strconv.ParseInt(env("MAILDEV_MAX_MESSAGE_BYTES", "26214400"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("MAILDEV_MAX_MESSAGE_BYTES: %w", err)
	}
	c.MaxMessageBytes = mb

	rawKey := env("MAILDEV_MASTER_KEY", "")
	if rawKey == "" {
		return nil, fmt.Errorf("MAILDEV_MASTER_KEY is required (base64 of 32 random bytes; generate with: openssl rand -base64 32)")
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("MAILDEV_MASTER_KEY must be base64 of exactly 32 bytes")
	}
	c.MasterKey = key

	if c.AuthMode != "ldap" && c.AuthMode != "dev" && c.AuthMode != "local" {
		return nil, fmt.Errorf("MAILDEV_AUTH_MODE must be ldap, local or dev")
	}
	if c.Edition != "full" && c.Edition != "lite" {
		return nil, fmt.Errorf("MAILDEV_EDITION must be full or lite")
	}
	return c, nil
}

// Lite reports the single-project edition: no directory, every account is
// local, local accounts may own the project and new projects cannot be created.
func (c *Config) Lite() bool { return c.Edition == "lite" }
