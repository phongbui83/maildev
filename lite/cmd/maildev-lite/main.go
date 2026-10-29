// Command maildev-lite is the single-binary, single-project edition of MailDev:
// SQLite storage, local accounts, the web UI embedded, HTTPS served directly.
// It reuses the servers, API and ingestion code of the full edition verbatim.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/bootstrap"
	"github.com/phongbui83/maildev/internal/config"
	"github.com/phongbui83/maildev/internal/events"
	"github.com/phongbui83/maildev/internal/httpapi"
	"github.com/phongbui83/maildev/internal/imapd"
	"github.com/phongbui83/maildev/internal/ingest"
	"github.com/phongbui83/maildev/internal/pop3d"
	"github.com/phongbui83/maildev/internal/secrets"
	"github.com/phongbui83/maildev/internal/smime"
	"github.com/phongbui83/maildev/internal/smtpd"
	"github.com/phongbui83/maildev/internal/store"
	"github.com/phongbui83/maildev/internal/tlsutil"
	"github.com/phongbui83/maildev/internal/webui"
)

var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println("maildev-lite", version)
			return
		case "reset-admin":
			if err := resetAdmin(os.Args[2:]); err != nil {
				slog.Error("reset-admin failed", "err", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func setDefault(key, val string) {
	if v, ok := os.LookupEnv(key); !ok || v == "" {
		os.Setenv(key, val)
	}
}

func firstDir(candidates ...string) string {
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return candidates[len(candidates)-1]
}

// masterKey returns MAILDEV_MASTER_KEY or the key persisted in the data dir,
// generating one on first start. Losing it makes stored credentials and relay
// secrets unreadable (mail itself is stored in clear in the lite edition).
func masterKey(dataDir string) (string, error) {
	if v := os.Getenv("MAILDEV_MASTER_KEY"); v != "" {
		return v, nil
	}
	p := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := base64.StdEncoding.EncodeToString(raw)
	if err := os.WriteFile(p, []byte(key+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	slog.Info("generated master key", "file", p)
	return key, nil
}

func run() error {
	dataDir := os.Getenv("MAILDEV_DATA_DIR")
	if dataDir == "" {
		dataDir = firstDir("/data", "data")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return err
	}
	certDir := os.Getenv("MAILDEV_CERT_DIR")
	if certDir == "" {
		certDir = firstDir("/certs", "certs")
	}
	key, err := masterKey(dataDir)
	if err != nil {
		return err
	}
	os.Setenv("MAILDEV_MASTER_KEY", key)

	// Lite defaults; every one can still be overridden through the environment.
	setDefault("MAILDEV_EDITION", "lite")
	setDefault("MAILDEV_AUTH_MODE", "local")
	setDefault("MAILDEV_DEMO_PROJECT", "false")
	setDefault("MAILDEV_HOSTNAME", "maildev.local")
	setDefault("MAILDEV_PUBLIC_URL", "https://"+os.Getenv("MAILDEV_HOSTNAME"))
	setDefault("MAILDEV_HTTP_ADDR", ":8080")
	setDefault("MAILDEV_SMTP_ADDRS", ":1025,:1587")
	setDefault("MAILDEV_SMTPS_ADDRS", ":1465")
	setDefault("MAILDEV_IMAP_ADDRS", ":1143")
	setDefault("MAILDEV_IMAPS_ADDRS", ":1993")
	setDefault("MAILDEV_POP3_ADDRS", ":1110")
	setDefault("MAILDEV_POP3S_ADDRS", ":1995")
	setDefault("MAILDEV_PUBLIC_SMTP_PORTS", "25,587")
	setDefault("MAILDEV_PUBLIC_SMTPS_PORTS", "465")
	setDefault("MAILDEV_PUBLIC_IMAP_PORTS", "143")
	setDefault("MAILDEV_PUBLIC_IMAPS_PORTS", "993")
	setDefault("MAILDEV_PUBLIC_POP3_PORTS", "110")
	setDefault("MAILDEV_PUBLIC_POP3S_PORTS", "995")
	if _, err := os.Stat(filepath.Join(certDir, "tls.crt")); err == nil {
		setDefault("MAILDEV_TLS_CERT", filepath.Join(certDir, "tls.crt"))
		setDefault("MAILDEV_TLS_KEY", filepath.Join(certDir, "tls.key"))
	}
	if _, err := os.Stat(filepath.Join(certDir, "smime-ca.pem")); err == nil {
		setDefault("MAILDEV_SMIME_CA_FILE", filepath.Join(certDir, "smime-ca.pem"))
	}
	httpsAddr := ":8443"
	if v, ok := os.LookupEnv("MAILDEV_HTTPS_ADDR"); ok {
		httpsAddr = v
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	box, err := secrets.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	dbPath := filepath.Join(dataDir, "maildev.db")
	st, err := store.Open(ctx, dbPath, box)
	if err != nil {
		return err
	}
	defer st.Close()
	st.SetLite(cfg.Lite())
	slog.Info("maildev-lite starting", "version", version, "edition", cfg.Edition, "db", dbPath, "auth", cfg.AuthMode)

	var authenticator auth.Authenticator
	switch cfg.AuthMode {
	case "dev":
		authenticator = auth.NewDev()
	case "local":
		authenticator = auth.NewLocalOnly()
	default:
		authenticator, err = auth.NewLDAP(cfg.LDAP)
		if err != nil {
			return err
		}
	}
	authSvc := auth.NewService(st, authenticator, cfg.SessionTTL, cfg.CookieSecure, cfg.AdminUsers)

	if cfg.Lite() {
		if err := ensureLite(ctx, st, cfg); err != nil {
			return err
		}
	}
	if cfg.DemoProject {
		if err := bootstrap.EnsureDemoProject(ctx, st); err != nil {
			return err
		}
	}

	bus := events.New()
	var trust *smime.Trust
	if len(cfg.SMIMECAURLs) > 0 || cfg.SMIMECAFile != "" || cfg.SMIMESystemRoots {
		trust = smime.NewTrust(cfg.SMIMECAURLs, cfg.SMIMECAFile, cfg.SMIMESystemRoots)
		if err := trust.Refresh(ctx); err != nil {
			slog.Warn("smime trust store not loaded, signatures will be reported unverified", "err", err)
		}
		go trust.RefreshLoop(ctx, time.Hour)
	}
	ing := ingest.New(st, bus, cfg.MaxMessageBytes, cfg.LogMailEvents, trust)

	tlsCfg, err := tlsutil.Config(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.Hostname)
	if err != nil {
		return err
	}

	api := httpapi.New(cfg, st, authSvc, ing, nil, nil)
	handler := route(api.Handler(), webui.Handler())

	errCh := make(chan error, 6)
	var servers []*http.Server
	if cfg.HTTPAddr != "" {
		srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
		servers = append(servers, srv)
		go func() {
			slog.Info("http listening", "addr", cfg.HTTPAddr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}
	if httpsAddr != "" {
		srv := &http.Server{Addr: httpsAddr, Handler: handler, TLSConfig: tlsCfg, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
		servers = append(servers, srv)
		go func() {
			slog.Info("https listening", "addr", httpsAddr, "cert", cfg.TLSCertFile)
			if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}
	go func() {
		errCh <- smtpd.Serve(ctx, smtpd.NewBackend(st, ing, cfg.Hostname), smtpd.Options{
			Hostname: cfg.Hostname, Addrs: cfg.SMTPAddrs, TLSAddrs: cfg.SMTPSAddrs, TLSConfig: tlsCfg, MaxMessageBytes: cfg.MaxMessageBytes,
		})
	}()
	go func() {
		errCh <- imapd.Serve(ctx, imapd.NewBackend(st, bus, ing), imapd.Options{Addrs: cfg.IMAPAddrs, TLSAddrs: cfg.IMAPSAddrs, TLSConfig: tlsCfg})
	}()
	go func() {
		errCh <- pop3d.Serve(ctx, pop3d.NewBackend(st, bus), pop3d.Options{Hostname: cfg.Hostname, Addrs: cfg.POP3Addrs, TLSAddrs: cfg.POP3SAddrs, TLSConfig: tlsCfg})
	}()
	go bootstrap.Housekeeping(ctx, st, 10*time.Minute)

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case err := <-errCh:
		if err != nil {
			slog.Error("server failed", "err", err)
			stop()
		}
		for _, s := range servers {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Shutdown(sctx)
			cancel()
		}
		return err
	}
	for _, s := range servers {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = s.Shutdown(sctx)
		cancel()
	}
	return nil
}

// route sends API and ingestion paths to the Go API, everything else to the UI.
func route(api, ui http.Handler) http.Handler {
	prefixes := []string{"/api", "/sendgrid/", "/v3/", "/ses", "/v2/", "/azure/", "/graph/", "/gmail/", "/healthz", "/readyz"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range prefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				api.ServeHTTP(w, r)
				return
			}
		}
		ui.ServeHTTP(w, r)
	})
}
