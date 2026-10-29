// Package model holds the domain types shared across stores, servers and the HTTP API.
package model

import (
	"time"

	"github.com/google/uuid"
)

const (
	RoleAdmin   = "admin"
	RoleAuditor = "auditor"
	RoleUser    = "user"

	MemberOwner  = "owner"
	MemberMember = "member"

	CredSMTP = "smtp"
	CredAPI  = "api"
	CredSES  = "ses"

	SourceSMTP     = "smtp"
	SourceAPI      = "api"
	SourceSendGrid = "sendgrid"
	SourceSES      = "ses"
	SourceAzure    = "azure"
	SourceGraph    = "graph"
	SourceGmail    = "gmail"
)

const (
	AuthLDAP  = "ldap"
	AuthLocal = "local"
)

type User struct {
	ID                 uuid.UUID  `json:"id"`
	LDAPUUID           *uuid.UUID `json:"ldap_uuid,omitempty"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	Email              string     `json:"email"`
	Department         string     `json:"department"`
	Role               string     `json:"role"`
	AuthSource         string     `json:"auth_source"`
	Disabled           bool       `json:"disabled"`
	MustChangePassword bool       `json:"must_change_password"`
	LockedUntil        *time.Time `json:"locked_until,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
}

func (u *User) IsAdmin() bool   { return u.Role == RoleAdmin }
func (u *User) IsAuditor() bool { return u.Role == RoleAuditor }
func (u *User) IsLocal() bool   { return u.AuthSource == AuthLocal }

type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
}

type Project struct {
	ID            uuid.UUID `json:"id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	AutoJoin      bool      `json:"auto_join"`
	RelayEnabled  bool      `json:"relay_enabled"`
	RetentionDays int       `json:"retention_days"`
	MaxEmails     int       `json:"max_emails"`
	UIDValidity   uint32    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Populated for list views.
	MyRole      string `json:"my_role,omitempty"`
	EmailCount  int    `json:"email_count"`
	UnreadCount int    `json:"unread_count"`
}

type Member struct {
	ProjectID   uuid.UUID `json:"project_id"`
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	AddedAt     time.Time `json:"added_at"`
	Pending     bool      `json:"pending"` // invite not yet materialized
}

type Credential struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label"`
	Username   string     `json:"username"`
	Secret     string     `json:"secret,omitempty"` // decrypted on demand
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type Address struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type Email struct {
	ID              uuid.UUID  `json:"id"`
	ProjectID       uuid.UUID  `json:"project_id"`
	UID             int64      `json:"uid"`
	CredentialID    *uuid.UUID `json:"credential_id,omitempty"`
	Source          string     `json:"source"`
	ClientIP        string     `json:"client_ip"`
	ReceivedAt      time.Time  `json:"received_at"`
	MessageID       string     `json:"message_id"`
	Subject         string     `json:"subject"`
	From            []Address  `json:"from"`
	To              []Address  `json:"to"`
	Cc              []Address  `json:"cc"`
	Bcc             []Address  `json:"bcc"`
	EnvelopeFrom    string     `json:"envelope_from"`
	EnvelopeTo      []string   `json:"envelope_to"`
	SentAt          *time.Time `json:"sent_at,omitempty"`
	Size            int        `json:"size"`
	TextBody        string     `json:"text_body,omitempty"`
	HTMLBody        string     `json:"html_body,omitempty"`
	Headers         []Header   `json:"headers,omitempty"`
	AttachmentCount int        `json:"attachment_count"`
	SMIME           string     `json:"smime,omitempty"` // "", signed, signed-opaque, encrypted
	SMIMEValid      *bool      `json:"smime_valid,omitempty"`
	SMIMESigner     string     `json:"smime_signer,omitempty"`
	SMIMEError      string     `json:"smime_error,omitempty"`
	Seen            bool       `json:"seen"`
	Flagged         bool       `json:"flagged"`
	Deleted         bool       `json:"deleted"`
	Snippet         string     `json:"snippet,omitempty"`

	RelayStatus      string     `json:"relay_status"`
	RelayRequestedBy string     `json:"relay_requested_by,omitempty"`
	RelayRequestedAt *time.Time `json:"relay_requested_at,omitempty"`
	RelayDecidedBy   string     `json:"relay_decided_by,omitempty"`
	RelayDecidedAt   *time.Time `json:"relay_decided_at,omitempty"`
	RelayError       string     `json:"relay_error,omitempty"`
	RelayRemoteID    string     `json:"relay_remote_id,omitempty"`
	RelayNote        string     `json:"relay_note,omitempty"`

	Attachments []Attachment `json:"attachments,omitempty"`
}

const (
	RelayNone     = "none"
	RelayPending  = "pending"
	RelaySent     = "sent"
	RelayFailed   = "failed"
	RelayRejected = "rejected"

	RelaySMTP     = "smtp"
	RelaySES      = "ses"
	RelaySendGrid = "sendgrid"
)

// RelayConfig is the admin-managed delivery target of a project. Secret is never
// serialized; HasSecret tells the UI whether one is stored.
type RelayConfig struct {
	ProjectID         uuid.UUID `json:"project_id"`
	Kind              string    `json:"kind"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	Security          string    `json:"security"`
	TLSInsecure       bool      `json:"tls_insecure"` // skip certificate verification (internal relays with private CAs)
	Username          string    `json:"username"`
	Secret            string    `json:"-"`
	HasSecret         bool      `json:"has_secret"`
	Region            string    `json:"region"`
	AccessKeyID       string    `json:"access_key_id"`
	Endpoint          string    `json:"endpoint"`
	FromOverride      string    `json:"from_override"`
	AllowedRecipients string    `json:"allowed_recipients"`
	UpdatedBy         string    `json:"updated_by,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Attachment struct {
	ID          uuid.UUID `json:"id"`
	EmailID     uuid.UUID `json:"email_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int       `json:"size"`
	ContentID   string    `json:"content_id"`
	Inline      bool      `json:"inline"`
	Data        []byte    `json:"-"`
}

type AuditEntry struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	ActorUserID *uuid.UUID     `json:"actor_user_id,omitempty"`
	Actor       string         `json:"actor"`
	ProjectID   *uuid.UUID     `json:"project_id,omitempty"`
	ProjectSlug string         `json:"project_slug,omitempty"`
	Action      string         `json:"action"`
	Target      string         `json:"target"`
	Detail      map[string]any `json:"detail"`
	IP          string         `json:"ip"`
	Protocol    string         `json:"protocol"`
}
