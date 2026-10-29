// Package smtpd is the capture SMTP server. Every message must be sent over an
// authenticated session; the credential decides which project receives it.
// Nothing is ever relayed.
package smtpd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/phongbui83/maildev/internal/ingest"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

type Backend struct {
	store    *store.Store
	ingest   *ingest.Ingestor
	hostname string
}

func NewBackend(st *store.Store, in *ingest.Ingestor, hostname string) *Backend {
	return &Backend{store: st, ingest: in, hostname: hostname}
}

func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	ip := ""
	if addr, ok := c.Conn().RemoteAddr().(*net.TCPAddr); ok {
		ip = addr.IP.String()
	}
	return &session{be: b, conn: c, ip: ip}, nil
}

type session struct {
	be   *Backend
	conn *smtp.Conn
	ip   string

	cred    *model.Credential
	project *model.Project
	from    string
	to      []string
}

var errAuthRequired = &smtp.SMTPError{Code: 530, EnhancedCode: smtp.EnhancedCode{5, 7, 0}, Message: "Authentication required: use your project's SMTP credentials"}
var errAuthFailed = &smtp.SMTPError{Code: 535, EnhancedCode: smtp.EnhancedCode{5, 7, 8}, Message: "Authentication credentials invalid"}

func (s *session) AuthMechanisms() []string {
	return []string{sasl.Plain, sasl.Login}
}

func (s *session) authenticate(username, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, tlsOn := s.conn.TLSConnectionState()
	cred, project, err := s.be.store.Authenticate(ctx, model.CredSMTP, username, password)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			slog.Warn("smtp auth failed", "user", username, "client", s.ip)
			s.be.store.Audit(ctx, store.AuditParams{Actor: username, Action: "smtp_login_failed", IP: s.ip, Protocol: "smtp", Detail: map[string]any{"tls": tlsOn}})
			return errAuthFailed
		}
		slog.Error("smtp auth error", "err", err)
		return &smtp.SMTPError{Code: 454, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Temporary authentication failure"}
	}
	s.cred, s.project = cred, project
	pid := project.ID
	s.be.store.Audit(ctx, store.AuditParams{Actor: username, ProjectID: &pid, Action: "smtp_login", IP: s.ip, Protocol: "smtp", Detail: map[string]any{"tls": tlsOn}})
	return nil
}

func (s *session) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			if identity != "" && identity != username {
				return errAuthFailed
			}
			return s.authenticate(username, password)
		}), nil
	case sasl.Login:
		return newLoginServer(s.authenticate), nil
	}
	return nil, fmt.Errorf("unsupported mechanism %q", mech)
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	if s.cred == nil {
		return errAuthRequired
	}
	s.from = from
	s.to = nil
	return nil
}

func (s *session) Rcpt(to string, opts *smtp.RcptOptions) error {
	if s.cred == nil {
		return errAuthRequired
	}
	s.to = append(s.to, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if s.cred == nil {
		return errAuthRequired
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	email, err := s.be.ingest.Ingest(ctx, ingest.Envelope{
		Project:      s.project,
		Credential:   s.cred,
		Source:       model.SourceSMTP,
		ClientIP:     s.ip,
		EnvelopeFrom: s.from,
		EnvelopeTo:   s.to,
	}, raw)
	if err != nil {
		if errors.Is(err, ingest.ErrTooLarge) {
			return &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 3, 4}, Message: "Message too large"}
		}
		slog.Error("smtp ingest failed", "project", s.project.Slug, "err", err)
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure storing message"}
	}
	return &smtp.SMTPError{Code: 250, EnhancedCode: smtp.EnhancedCode{2, 0, 0}, Message: "OK: captured as " + email.ID.String()}
}

func (s *session) Reset()        { s.from, s.to = "", nil }
func (s *session) Logout() error { return nil }

// loginServer implements the legacy AUTH LOGIN mechanism (still used by many
// .NET, PHP and Java mail clients).
type loginServer struct {
	auth     func(username, password string) error
	step     int
	username string
}

func newLoginServer(auth func(username, password string) error) sasl.Server {
	return &loginServer{auth: auth}
}

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.step {
	case 0:
		l.step = 1
		if len(response) > 0 { // initial response carries the username
			l.username = string(response)
			l.step = 2
			return []byte("Password:"), false, nil
		}
		return []byte("Username:"), false, nil
	case 1:
		l.username = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	case 2:
		l.step = 3
		return nil, true, l.auth(l.username, string(response))
	}
	return nil, true, errors.New("unexpected response")
}

// Options controls listeners.
type Options struct {
	Hostname        string
	Addrs           []string // plaintext + STARTTLS
	TLSAddrs        []string // implicit TLS
	TLSConfig       *tls.Config
	MaxMessageBytes int64
}

// Serve starts all listeners and blocks until ctx is done.
func Serve(ctx context.Context, be *Backend, o Options) error {
	var servers []*smtp.Server
	mk := func() *smtp.Server {
		s := smtp.NewServer(be)
		s.Domain = o.Hostname
		s.TLSConfig = o.TLSConfig
		s.AllowInsecureAuth = true // dev clients commonly send AUTH without TLS
		s.MaxMessageBytes = o.MaxMessageBytes
		s.MaxRecipients = 200
		s.ReadTimeout = 2 * time.Minute
		s.WriteTimeout = 2 * time.Minute
		s.EnableSMTPUTF8 = true
		return s
	}
	errCh := make(chan error, len(o.Addrs)+len(o.TLSAddrs))
	for _, addr := range o.Addrs {
		s := mk()
		s.Addr = addr
		servers = append(servers, s)
		go func(s *smtp.Server) {
			slog.Info("smtp listening", "addr", s.Addr, "mode", "plain+starttls")
			if err := s.ListenAndServe(); err != nil && !isClosed(err) {
				errCh <- fmt.Errorf("smtp %s: %w", s.Addr, err)
			}
		}(s)
	}
	for _, addr := range o.TLSAddrs {
		s := mk()
		s.Addr = addr
		servers = append(servers, s)
		go func(s *smtp.Server) {
			slog.Info("smtp listening", "addr", s.Addr, "mode", "implicit-tls")
			if err := s.ListenAndServeTLS(); err != nil && !isClosed(err) {
				errCh <- fmt.Errorf("smtps %s: %w", s.Addr, err)
			}
		}(s)
	}
	select {
	case <-ctx.Done():
		for _, s := range servers {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.Shutdown(sctx)
			cancel()
		}
		return nil
	case err := <-errCh:
		for _, s := range servers {
			_ = s.Close()
		}
		return err
	}
}

func isClosed(err error) bool {
	return errors.Is(err, smtp.ErrServerClosed) || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
