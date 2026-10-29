-- Directory users are pre-imported (ldap_uuid is filled at their first login).
ALTER TABLE users ADD COLUMN department text NOT NULL DEFAULT '';
CREATE INDEX users_department_idx ON users(department);
