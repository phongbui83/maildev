-- Per-project envelope encryption (OpenBao Transit wraps a project data key).
ALTER TABLE projects ADD COLUMN dek_wrapped text NOT NULL DEFAULT '';

ALTER TABLE emails
    ADD COLUMN encrypted boolean NOT NULL DEFAULT false,
    ADD COLUMN body_enc  bytea;           -- AES-GCM(JSON{text,html}) when encrypted
ALTER TABLE attachments ADD COLUMN encrypted boolean NOT NULL DEFAULT false;
CREATE INDEX emails_unencrypted_idx ON emails(project_id) WHERE NOT encrypted;

-- Local (non-AD) accounts for external customers.
ALTER TABLE users
    ALTER COLUMN ldap_uuid DROP NOT NULL,
    ADD COLUMN auth_source          text NOT NULL DEFAULT 'ldap' CHECK (auth_source IN ('ldap', 'local')),
    ADD COLUMN password_hash        text NOT NULL DEFAULT '',
    ADD COLUMN must_change_password boolean NOT NULL DEFAULT false,
    ADD COLUMN failed_logins        integer NOT NULL DEFAULT 0,
    ADD COLUMN locked_until         timestamptz,
    ADD COLUMN created_by           uuid REFERENCES users(id) ON DELETE SET NULL;
-- Local accounts can never hold a global role.
ALTER TABLE users ADD CONSTRAINT users_local_role_chk CHECK (auth_source = 'ldap' OR role = 'user');
