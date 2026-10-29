CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ldap_uuid     uuid UNIQUE NOT NULL,
    username      text UNIQUE NOT NULL,
    display_name  text NOT NULL DEFAULT '',
    email         text NOT NULL DEFAULT '',
    role          text NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'auditor', 'user')),
    disabled      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

CREATE TABLE sessions (
    id         text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    ip         text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);

CREATE TABLE projects (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug           text UNIQUE NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    auto_join      boolean NOT NULL DEFAULT false,
    retention_days integer NOT NULL DEFAULT 30 CHECK (retention_days >= 1),
    max_emails     integer NOT NULL DEFAULT 5000 CHECK (max_emails >= 10),
    uid_validity   integer NOT NULL DEFAULT (extract(epoch FROM now()))::integer,
    next_uid       bigint NOT NULL DEFAULT 1,
    created_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner', 'member')),
    added_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user_idx ON project_members(user_id);

-- Members added before their first login; materialized into project_members at login.
CREATE TABLE project_invites (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    username   text NOT NULL,
    role       text NOT NULL CHECK (role IN ('owner', 'member')),
    invited_by uuid REFERENCES users(id) ON DELETE SET NULL,
    invited_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, username)
);

CREATE TABLE project_credentials (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('smtp', 'api', 'ses')),
    label        text NOT NULL DEFAULT '',
    username     text NOT NULL,
    secret_enc   bytea NOT NULL,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_used_at timestamptz
);
CREATE UNIQUE INDEX project_credentials_active_username ON project_credentials(kind, username) WHERE revoked_at IS NULL;
CREATE INDEX project_credentials_project_idx ON project_credentials(project_id);

CREATE TABLE emails (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id       uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    uid              bigint NOT NULL,
    credential_id    uuid REFERENCES project_credentials(id) ON DELETE SET NULL,
    source           text NOT NULL,
    client_ip        text NOT NULL DEFAULT '',
    received_at      timestamptz NOT NULL DEFAULT now(),
    message_id       text NOT NULL DEFAULT '',
    subject          text NOT NULL DEFAULT '',
    from_addrs       jsonb NOT NULL DEFAULT '[]',
    to_addrs         jsonb NOT NULL DEFAULT '[]',
    cc_addrs         jsonb NOT NULL DEFAULT '[]',
    bcc_addrs        jsonb NOT NULL DEFAULT '[]',
    envelope_from    text NOT NULL DEFAULT '',
    envelope_to      text[] NOT NULL DEFAULT '{}',
    sent_at          timestamptz,
    size             integer NOT NULL,
    text_body        text NOT NULL DEFAULT '',
    html_body        text NOT NULL DEFAULT '',
    headers          jsonb NOT NULL DEFAULT '[]',
    attachment_count integer NOT NULL DEFAULT 0,
    seen             boolean NOT NULL DEFAULT false,
    flagged          boolean NOT NULL DEFAULT false,
    deleted          boolean NOT NULL DEFAULT false,
    raw              bytea NOT NULL,
    addr_text        text NOT NULL DEFAULT '',
    search           tsvector GENERATED ALWAYS AS (
                         to_tsvector('simple', coalesce(subject, '') || ' ' || coalesce(addr_text, '') || ' ' || left(coalesce(text_body, ''), 100000))
                     ) STORED,
    UNIQUE (project_id, uid)
);
CREATE INDEX emails_project_received_idx ON emails(project_id, received_at DESC);
CREATE INDEX emails_search_idx ON emails USING gin(search);

CREATE TABLE attachments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email_id     uuid NOT NULL REFERENCES emails(id) ON DELETE CASCADE,
    filename     text NOT NULL DEFAULT '',
    content_type text NOT NULL DEFAULT 'application/octet-stream',
    size         integer NOT NULL,
    content_id   text NOT NULL DEFAULT '',
    inline       boolean NOT NULL DEFAULT false,
    data         bytea NOT NULL
);
CREATE INDEX attachments_email_idx ON attachments(email_id);

CREATE TABLE audit_log (
    id            bigserial PRIMARY KEY,
    at            timestamptz NOT NULL DEFAULT now(),
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    actor         text NOT NULL DEFAULT '',
    project_id    uuid REFERENCES projects(id) ON DELETE SET NULL,
    action        text NOT NULL,
    target        text NOT NULL DEFAULT '',
    detail        jsonb NOT NULL DEFAULT '{}',
    ip            text NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_project_idx ON audit_log(project_id, at DESC);
CREATE INDEX audit_log_at_idx ON audit_log(at DESC);
