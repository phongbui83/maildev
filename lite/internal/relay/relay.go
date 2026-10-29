// Package relay delivers an approved captured message to a real provider:
// an upstream SMTP server, AWS SES (SigV4-signed SESv2 raw send) or SendGrid.
// It is only ever invoked after an owner approved the specific message.
package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/phongbui83/maildev/internal/model"
)

var ErrRecipientNotAllowed = errors.New("recipient not allowed by relay policy")

// Result is what the provider told us.
type Result struct {
	RemoteID string
	Detail   string
}

// Recipients returns the delivery list: the SMTP envelope when present, else the
// header recipients.
func Recipients(e *model.Email) []string {
	seen := map[string]bool{}
	var out []string
	add := func(a string) {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, a := range e.EnvelopeTo {
		add(a)
	}
	if len(out) == 0 {
		for _, list := range [][]model.Address{e.To, e.Cc, e.Bcc} {
			for _, a := range list {
				add(a.Address)
			}
		}
	}
	return out
}

// Sender returns the envelope sender: FromOverride, else the captured envelope
// sender, else the From header.
func Sender(cfg *model.RelayConfig, e *model.Email) string {
	if cfg.FromOverride != "" {
		if a, err := mail.ParseAddress(cfg.FromOverride); err == nil {
			return a.Address
		}
		return cfg.FromOverride
	}
	if e.EnvelopeFrom != "" {
		return e.EnvelopeFrom
	}
	if len(e.From) > 0 {
		return e.From[0].Address
	}
	return ""
}

// CheckRecipients enforces the optional allow-list: comma separated patterns such
// as "*@example.com", "qa-*@example.com" or exact addresses. Empty allows everyone.
func CheckRecipients(cfg *model.RelayConfig, rcpts []string) error {
	pats := splitList(cfg.AllowedRecipients)
	if len(pats) == 0 {
		return nil
	}
	for _, r := range rcpts {
		ok := false
		for _, p := range pats {
			if m, _ := path.Match(strings.ToLower(p), strings.ToLower(r)); m {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%w: %s", ErrRecipientNotAllowed, r)
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == ' ' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Send delivers the message according to the config kind.
func Send(ctx context.Context, cfg *model.RelayConfig, e *model.Email, raw []byte) (*Result, error) {
	rcpts := Recipients(e)
	if len(rcpts) == 0 {
		return nil, errors.New("message has no recipients")
	}
	if err := CheckRecipients(cfg, rcpts); err != nil {
		return nil, err
	}
	from := Sender(cfg, e)
	if from == "" {
		return nil, errors.New("message has no sender address")
	}
	switch cfg.Kind {
	case model.RelaySMTP:
		return sendSMTP(ctx, cfg, from, rcpts, raw)
	case model.RelaySES:
		return sendSES(ctx, cfg, from, rcpts, raw)
	case model.RelaySendGrid:
		return sendSendGrid(ctx, cfg, from, e)
	}
	return nil, fmt.Errorf("unknown relay kind %q", cfg.Kind)
}

// Validate checks the configuration shape before it is stored.
func Validate(cfg *model.RelayConfig) error {
	switch cfg.Kind {
	case model.RelaySMTP:
		if cfg.Host == "" {
			return errors.New("smtp host is required")
		}
		if cfg.Port <= 0 || cfg.Port > 65535 {
			return errors.New("smtp port is invalid")
		}
		switch cfg.Security {
		case "none", "starttls", "tls":
		default:
			return errors.New("security must be none, starttls or tls")
		}
	case model.RelaySES:
		if cfg.Region == "" && cfg.Endpoint == "" {
			return errors.New("ses region (or endpoint) is required")
		}
		if cfg.AccessKeyID == "" {
			return errors.New("ses access key id is required")
		}
	case model.RelaySendGrid:
	default:
		return errors.New("kind must be smtp, ses or sendgrid")
	}
	if cfg.FromOverride != "" {
		if _, err := mail.ParseAddress(cfg.FromOverride); err != nil {
			return errors.New("from override is not a valid address")
		}
	}
	for _, p := range splitList(cfg.AllowedRecipients) {
		if _, err := path.Match(p, "x"); err != nil {
			return fmt.Errorf("allowed recipient pattern %q is invalid", p)
		}
	}
	return nil
}

// --- SMTP ---

func smtpDial(ctx context.Context, cfg *model.RelayConfig) (*smtp.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tcfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecure} //nolint:gosec // admin-chosen for internal relays
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	var c *smtp.Client
	switch cfg.Security {
	case "tls":
		c = smtp.NewClient(tls.Client(conn, tcfg))
	case "starttls":
		c, err = smtp.NewClientStartTLS(conn, tcfg)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	default:
		c = smtp.NewClient(conn)
	}
	if cfg.Username != "" {
		var a sasl.Client
		if c.SupportsAuth(sasl.Plain) {
			a = sasl.NewPlainClient("", cfg.Username, cfg.Secret)
		} else {
			a = sasl.NewLoginClient(cfg.Username, cfg.Secret)
		}
		if err := c.Auth(a); err != nil {
			c.Close()
			return nil, fmt.Errorf("auth: %w", err)
		}
	}
	return c, nil
}

func sendSMTP(ctx context.Context, cfg *model.RelayConfig, from string, rcpts []string, raw []byte) (*Result, error) {
	c, err := smtpDial(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if err := c.SendMail(from, rcpts, strings.NewReader(string(raw))); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	_ = c.Quit()
	return &Result{Detail: fmt.Sprintf("accepted by %s for %d recipient(s)", cfg.Host, len(rcpts))}, nil
}

// TestSMTP connects and authenticates without sending anything.
func TestSMTP(ctx context.Context, cfg *model.RelayConfig) error {
	c, err := smtpDial(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}
