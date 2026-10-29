# Architecture

This note is for contributors. It describes how the pieces fit together and the invariants that must survive changes.

## Components

One Go binary (`backend/cmd/maildev`) runs four listeners that share one PostgreSQL store:

- `internal/smtpd`: go-smtp server. AUTH (PLAIN/LOGIN) is mandatory; the SMTP credential maps to exactly one project, which is how tenancy works. Recipient addresses are irrelevant for routing. Unauthenticated `MAIL FROM` gets `530`.
- `internal/imapd`: go-imap v2 server exposing each project as a single `INBOX` (login = the project's SMTP credential). Sessions keep a UID-ordered snapshot and reconcile with the database on Poll/Idle (EXPUNGE/EXISTS/FLAGS). APPEND into INBOX is captured like any other mail; other mailboxes do not exist.
- `internal/pop3d`: hand-written POP3 server (USER/PASS, STLS, STAT/LIST/UIDL/RETR/TOP/DELE). UIDL is the email id; DELE deletes the message from the project at QUIT.
- `internal/httpapi`: chi router. `/api/*` is the session-cookie REST API for the UI. `/api/v1/send`, `/sendgrid/v3/mail/send`, `/ses` (v1 query API) and `/ses/v2/email/outbound-emails` are credential-authenticated ingestion adapters that build a MIME message (`internal/mail.Build`) and feed it to the ingestor. SES SigV4 is not verified: the access key id only selects the project. `send_cloud.go` adds the cloud-SDK shapes keyed by the API key id in the path: `/azure/{keyID}/emails:send` (ACS HMAC-SHA256 verified against the secret), `/graph/{keyID}/v1.0/users/{user}/sendMail` and `/me/sendMail` (Bearer = full key or bare secret), `/gmail/{keyID}/gmail/v1/users/{user}/messages/send` (JSON raw base64url) and `/upload/...` (message/rfc822). Sources `azure`/`graph`/`gmail` are recorded on the email. New adapters need a prefix in `frontend/next.config.ts` (rewrites), `deploy/k8s/httproute.yaml` and `lite/cmd/maildev-lite/main.go` (`route`).
- `internal/ingest`: the one path every captured message takes: size check, parse (`internal/mail.Parse`, CRLF normalisation, S/MIME detection), insert (`store.InsertEmail`, allocating the per-project IMAP UID), publish on the in-process `events.Bus` (SSE + IMAP IDLE). Because the bus is in-process the API runs with one replica.

## Storage

Schema in `backend/internal/db/migrations/*.sql`, applied at startup in filename order and tracked in `schema_migrations`. Raw messages and attachments live in PostgreSQL (`bytea`); `emails.search` is a generated tsvector. Retention (`retention_days`, default 14, hard cap 180 via a DB constraint; `max_emails`) can only be changed by admins and is enforced every 10 minutes by `store.ApplyRetention` from the housekeeping goroutine, which writes a `retention_cleanup` audit entry when it deletes anything.

Credentials (`store/credentials.go`): `InitCredentials` revokes all active ones and issues three: `smtp` (username = project slug, also IMAP/POP3), `api` (full key is `<username>.<secret>`), `ses` (AKIA-style id + secret). Secrets are stored AES-GCM encrypted with `MAILDEV_MASTER_KEY` so the UI can show them again; losing the key makes stored credentials unreadable.

Handlers never touch the database driver directly: every query is a store method. The lite edition re-implements the whole store API on SQLite (`lite/internal/store`), so a new store method must be added there too.

## Auth and authorization

- Login binds to the directory over LDAPS as `<user>@<UPN suffix>`, then reads the user's own entry for `objectGUID` (converted with AD byte order, see `ADGUIDToUUID`), the stable key in `users.ldap_uuid`. A service bind DN is optional and only enables username lookup. `MAILDEV_AUTH_MODE=dev` replaces the directory with a stub; `local` disables it (password accounts only).
- Global roles: `admin` (everything, including the Logs tab = `/api/admin/audit`), `auditor` (reads every project and the global log, no writes), `user`. Project roles: `owner` (members, credentials, settings, relay approval), `member` (read, mark read, delete). `loadProject` middleware computes an `access` value; `requireWrite`/`requireManage`/`requireActive` gate handlers. Non-members get 404.
- Archived projects: credentials stop authenticating, the inbox is read-only, credential rotation and relay decisions are blocked and `ApplyRetention` skips them.
- Directory users can be pre-imported (`store.SyncDirectoryUsers` on every directory refresh) so members can be added before their first login; `ldap_uuid` is NULL until then. If a username comes back with a different objectGUID (account re-created for a new hire), `resetReusedUsername` resets the row to role `user`, removes memberships/invites/sessions and writes `user_identity_reset`.
- Local accounts (`auth/password.go`, `httpapi/users_local.go`): argon2id hashes, 5 failures lock for 15 minutes, `must_change_password` forces the `/password` page, a password change ends other sessions. In the full edition they stay on the `user` role (DB constraint) and can only be members; `MAILDEV_EDITION=lite` lifts both rules.
- Members added before their first login are stored in `project_invites` and materialised at login; projects with `auto_join` (the seeded `demo` project) add every user as member and admins as owners.

## Relay

Off by default and never automatic. Admins store a per-project target in `project_relay_config` (kind `smtp` | `ses` | `sendgrid`; secret encrypted like credentials, never returned by the API) and flip `projects.relay_enabled`. While enabled, `ingest` inserts every captured mail with `relay_status = pending`; members may re-request failed/rejected ones; owners/admins approve (single or batch) which calls `relay.Send` synchronously and records `sent`/`failed` plus actor, note and remote id. SMTP uses the go-smtp client (`none`/`starttls`/`tls`, optional `tls_insecure`); SES posts a raw message to the SESv2 API with a hand-written SigV4 signer; SendGrid rebuilds the message as a v3 JSON request. `allowed_recipients` glob patterns are enforced before any send; `from_override` replaces the envelope sender. Every step writes an audit action prefixed `relay_`.

## At-rest encryption

Enabled when `MAILDEV_OPENBAO_ADDR` is set (AppRole or token). `encryption.Service` keeps one AES-GCM cipher per project: on first use it creates Transit key `maildev-<slug>`, generates a 32-byte DEK, wraps it and stores it in `projects.dek_wrapped`; later it unwraps and caches. `store.InsertEmail` encrypts raw, a JSON of text+html (`body_enc`) and attachment data **before** opening its transaction (wrapping a new key updates the project row that `NextUID` locks: doing it inside the transaction deadlocks). Reads decrypt transparently; `EncryptBacklog` converts rows stored before encryption was on; project delete calls `Shred`, which deletes the Transit key. The tsvector only covers subject/addresses for encrypted mail.

## Audit and S/MIME

`store.Audit` never fails the caller. Besides web actions, `ingest` writes `mail_captured` for every message (protocol = source), and the SMTP/IMAP/POP3 servers and API-key checks write `*_login`, `*_login_failed`, `api_auth_failed` with client IP and TLS flag; the `protocol` column is filterable. Mail parsing tags S/MIME structure in `emails.smime` (signed / signed-opaque / encrypted); `internal/smime` verifies signatures with smallstep/pkcs7 against a `Trust` pool built from `MAILDEV_SMIME_CA_URLS` (PKI `ca_chain` endpoints, refreshed hourly), `MAILDEV_SMIME_CA_FILE` and optionally the OS roots. Detached verification needs the exact bytes of the first MIME part, so `verifyDetached` splits the raw body on the boundary itself. Nothing is ever decrypted.

## Preview safety

HTML is sanitised with bluemonday (`internal/mail/sanitize.go`), `cid:` references are rewritten to the attachment endpoint, and the preview response carries a CSP that blocks remote resources unless `?remote=1`; the UI renders it in a `sandbox=""` iframe. Attachments are served with `nosniff` and HTML/SVG/XML types are forced to download.

## Frontend

All pages are client components under `frontend/app`; `lib/api.ts` is the typed fetch wrapper, `lib/auth.tsx` the session context, `lib/config.tsx` the shared `/api/config` hook (`useLite()`), `components/Shell.tsx` the authenticated layout. Project pages share `components/ProjectNav.tsx` and `lib/project.tsx`: `/projects/[slug]` is a hub, `/dev` = `CredentialsPanel` + `SetupGuide` (data-driven provider snippets), `/inbox` = two-pane mailbox, `/relay` = approval queue, `/manage` = members/settings/relay target/audit. Routes accept the slug or the id. i18n: every string goes through `useT().t("key")` with keys in `lib/translations.ts` for `en`, `vi`, `ja`; the first render is always English to avoid hydration mismatches. `NEXT_OUTPUT=export` builds the static site embedded by the lite edition (`app/projects/[id]/layout.tsx` provides `generateStaticParams`; pages using `useSearchParams` sit in `Suspense`).

The user guide under `frontend/public/guide` is generated by `frontend/scripts/guide` (HTML from Python, screenshots from Playwright with numbered highlight boxes).

## Lite edition

`lite/` is a second Go module with the same import path. `lite/internal/*` except `store` and `webui` is a verbatim copy of `backend/internal/*` (`lite/scripts/sync-from-full.sh`); `store` is the SQLite implementation (modernc.org/sqlite, WAL, `_txlock=immediate`, times written in UTC and read through tolerant scanners because expressions lose the declared column type), `webui` serves the embedded static export and maps `/projects/<any>/<page>` onto the pre-rendered `default` project shell. `cmd/maildev-lite` applies the lite defaults (local auth, one auto-join project, `admin/admin` with forced change, HTTPS served directly) and offers `reset-admin`. `store.SetLite(true)` relaxes the two local-account rules; with `MAILDEV_EDITION=full` the lite binary behaves like the full edition, which is how the backend's end-to-end suites run against it (`lite/scripts/test.sh`).
