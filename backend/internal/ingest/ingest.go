// Package ingest is the single entry point through which every captured message
// (SMTP or API) is parsed, stored and announced.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/events"
	"github.com/phongbui83/maildev/internal/mail"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/smime"
	"github.com/phongbui83/maildev/internal/store"
)

type Ingestor struct {
	store    *store.Store
	bus      *events.Bus
	maxBytes int64
	logMail  bool
	trust    *smime.Trust // may be nil: signatures are then reported as unverified
}

func New(st *store.Store, bus *events.Bus, maxBytes int64, logMail bool, trust *smime.Trust) *Ingestor {
	return &Ingestor{store: st, bus: bus, maxBytes: maxBytes, logMail: logMail, trust: trust}
}

type Envelope struct {
	Project      *model.Project
	Credential   *model.Credential
	Source       string
	ClientIP     string
	EnvelopeFrom string
	EnvelopeTo   []string
}

var ErrTooLarge = fmt.Errorf("message exceeds maximum size")

// Ingest parses and stores raw; nothing is ever relayed anywhere.
func (in *Ingestor) Ingest(ctx context.Context, env Envelope, raw []byte) (*model.Email, error) {
	if in.maxBytes > 0 && int64(len(raw)) > in.maxBytes {
		return nil, ErrTooLarge
	}
	raw = mail.NormalizeCRLF(raw)
	parsed := mail.Parse(raw)
	e := &parsed.Email
	if e.SMIME == "signed" || e.SMIME == "signed-opaque" {
		res := smime.Verify(raw, e.SMIME, in.trust, time.Now())
		v := res.Valid
		e.SMIMEValid, e.SMIMESigner, e.SMIMEError = &v, res.Signer, res.Error
		if e.SMIME == "signed-opaque" && len(res.Content) > 0 {
			// Make the signed content readable in the UI (the raw message stays untouched).
			inner := mail.Parse(res.Content)
			e.TextBody, e.HTMLBody = inner.Email.TextBody, inner.Email.HTMLBody
			parsed.Attachments = inner.Attachments
		}
	}
	e.ProjectID = env.Project.ID
	if env.Credential != nil {
		id := env.Credential.ID
		e.CredentialID = &id
	}
	e.Source = env.Source
	e.ClientIP = env.ClientIP
	e.EnvelopeFrom = env.EnvelopeFrom
	e.EnvelopeTo = env.EnvelopeTo
	if e.EnvelopeTo == nil {
		e.EnvelopeTo = []string{}
	}

	stored, err := in.store.InsertEmail(ctx, store.InsertEmailParams{Email: e, Attachments: parsed.Attachments, RelayPending: env.Project.RelayEnabled}, raw)
	if err != nil {
		return nil, err
	}
	actor := "anonymous"
	if env.Credential != nil {
		actor = env.Credential.Username
	}
	pid := env.Project.ID
	in.store.Audit(ctx, store.AuditParams{
		Actor: actor, ProjectID: &pid, Action: "mail_captured", Target: stored.ID.String(), IP: env.ClientIP, Protocol: env.Source,
		Detail: map[string]any{"from": e.EnvelopeFrom, "to": e.EnvelopeTo, "subject": e.Subject, "size": len(raw), "relay_pending": env.Project.RelayEnabled},
	})
	if in.logMail {
		slog.Info("captured email", "project", env.Project.Slug, "source", env.Source, "from", e.EnvelopeFrom,
			"to", e.EnvelopeTo, "subject", e.Subject, "size", len(raw), "client", env.ClientIP)
	}
	summary := *stored
	summary.TextBody, summary.HTMLBody, summary.Headers = "", "", nil
	in.bus.Publish(events.Event{Type: "new", ProjectID: stored.ProjectID, EmailID: stored.ID, UID: stored.UID, Payload: summary})
	return stored, nil
}

func (in *Ingestor) Bus() *events.Bus { return in.bus }

// Notify is used by HTTP handlers after mutating an inbox.
func (in *Ingestor) Notify(typ string, projectID, emailID uuid.UUID, uid int64) {
	in.bus.Publish(events.Event{Type: typ, ProjectID: projectID, EmailID: emailID, UID: uid})
}
