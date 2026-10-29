-- Optional, admin-controlled relay: captured mail may be forwarded to a real
-- provider only after an owner approves each message.
ALTER TABLE projects ADD COLUMN relay_enabled boolean NOT NULL DEFAULT false;

CREATE TABLE project_relay_config (
    project_id         uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    kind               text NOT NULL CHECK (kind IN ('smtp', 'ses', 'sendgrid')),
    host               text NOT NULL DEFAULT '',
    port               integer NOT NULL DEFAULT 587,
    security           text NOT NULL DEFAULT 'starttls' CHECK (security IN ('none', 'starttls', 'tls')),
    tls_insecure       boolean NOT NULL DEFAULT false,
    username           text NOT NULL DEFAULT '',
    secret_enc         bytea,
    region             text NOT NULL DEFAULT '',
    access_key_id      text NOT NULL DEFAULT '',
    endpoint           text NOT NULL DEFAULT '',
    from_override      text NOT NULL DEFAULT '',
    allowed_recipients text NOT NULL DEFAULT '',
    updated_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE emails
    ADD COLUMN relay_status       text NOT NULL DEFAULT 'none'
        CHECK (relay_status IN ('none', 'pending', 'sent', 'failed', 'rejected')),
    ADD COLUMN relay_requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN relay_requested_at timestamptz,
    ADD COLUMN relay_decided_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN relay_decided_at   timestamptz,
    ADD COLUMN relay_error        text NOT NULL DEFAULT '',
    ADD COLUMN relay_remote_id    text NOT NULL DEFAULT '',
    ADD COLUMN relay_note         text NOT NULL DEFAULT '';

CREATE INDEX emails_relay_pending_idx ON emails(project_id, relay_requested_at DESC) WHERE relay_status <> 'none';
