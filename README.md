# MailDev

**Multi-project mail catcher for development and testing.** Applications send mail here instead of a real SMTP server or SES/SendGrid/Azure/Graph/Gmail; every message is stored under the project whose credentials were used and is **never delivered anywhere** unless an owner explicitly approves a relay. Teams read their inbox in the web UI or in Outlook over IMAP/POP3.

> Vietnamese version: [README.vi.md](README.vi.md). Architecture notes for contributors: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

![MailDev inbox](frontend/public/guide/img/en/08-inbox.png)

## Why

During development and testing applications still send e-mail (sign-up confirmations, password resets, invoices, batch reports). When test data is copied from production, the recipients are **real customers**: one batch run or one wrong click and test mail leaves the company, unnoticed and untraceable. Personal mail catchers (MailDev, Mailpit on a developer's laptop) cannot be shared with QA and PMs, and a shared instance lets every project see every other project's mail.

MailDev fixes exactly that: **one isolated mailbox per project**, selected by the credential the application authenticates with, readable by the project's members only, with every login, capture, download and relay decision in an audit log.

## Features

- **Isolated projects**: SMTP AUTH (PLAIN/LOGIN) is mandatory and the credential decides the project. Recipient addresses are irrelevant, so mail accidentally addressed to real people is simply captured.
- **Protocols**: SMTP (25 plain/STARTTLS, 587 STARTTLS, 465 TLS), IMAP (143 STARTTLS, 993 TLS) with a single `INBOX` per project, POP3 (110 STLS, 995 TLS). Works with Outlook, Thunderbird, Apple Mail.
- **Provider-compatible APIs**: keep the SDK, change the endpoint and the key. Generic JSON/raw endpoint, SendGrid v3 `mail/send`, AWS SES v1 (`SendEmail`, `SendRawEmail`) and v2 (`outbound-emails`), Azure Communication Services Email (HMAC-SHA256 verified), Microsoft Graph `sendMail`, Gmail API `messages.send` (JSON and media upload).
- **Roles**: global `admin` (projects, owners, users, settings, logs) and `auditor` (read-only everywhere plus the global log); per project `owner` (members, credentials, settings, relay approval) and `member` (read, mark read, delete). Non-members get 404.
- **Sign-in**: Active Directory over LDAPS (the `objectGUID` is the stable user key, username reuse is detected), a local-only mode with password accounts (argon2id, lockout after 5 failures, forced password change), or a `dev` stub for trials. Optional company directory import so every employee can be added as a member before their first login.
- **Approved relay** (optional, per project): an admin configures a real target (SMTP, AWS SES or SendGrid) and switches relay on; captured mail then waits in a queue until an owner approves it, one by one or in batch, with recipient allow-list and sender override. Everything is audited.
- **At-rest encryption** (optional): each project gets a data key wrapped by its own OpenBao/Vault Transit key; raw messages, bodies and attachments are AES-GCM encrypted in PostgreSQL while metadata stays searchable. Deleting a project destroys its key.
- **S/MIME**: signatures (detached and opaque) are verified against configured CA chains; encrypted messages are stored untouched for the client to decrypt.
- **Safe preview**: sanitized HTML in a sandboxed iframe, remote images blocked by default, inline `cid:` images served from stored attachments, phone/tablet/desktop widths, raw `.eml` download. HTML/SVG attachments are forced to download.
- **Audit log** with actor, source IP, timestamp and protocol for every web action, SMTP/IMAP/POP3 login, API-key use, captured message and relay decision. Per-project log for owners, global log for admins and auditors.
- **Retention**: 14 days by default (admins set 1 to 180 days and a message cap), cleanup every 10 minutes, archived projects are read-only and exempt.
- **Live updates** (Server-Sent Events, IMAP IDLE), **UI in English, Vietnamese and Japanese**, light and dark themes, a built-in illustrated user guide (`/guide/`).
- **Two editions**: the full edition (PostgreSQL, directory sign-in, Kubernetes manifests) and the **lite edition**, a single container with SQLite and local accounts for air-gapped customer sites (see [`lite/`](lite/README.md)).

## Quick start (Docker Compose)

Requirements: Docker 24+ with the compose plugin.

```bash
git clone https://github.com/phongbui83/maildev.git && cd maildev
cp .env.example .env
sed -i "s#^MAILDEV_MASTER_KEY=.*#MAILDEV_MASTER_KEY=$(openssl rand -base64 32)#" .env   # or edit by hand
docker compose up --build -d
```

Open <http://localhost:3000>. The default `.env` runs in `dev` auth mode: sign in with **any username and the password `dev`**; the user `admin` is administrator. A `demo` project exists and every user joins it automatically.

| Service | Host port (change in `.env`) |
|---|---|
| Web UI (proxies the API) | 3000 |
| API (REST + provider endpoints) | 8080 |
| SMTP plain/STARTTLS, submission, SMTPS | 1025, 1587, 1465 |
| IMAP, IMAPS | 1143, 1993 |
| POP3, POP3S | 1110, 1995 |

Send a first message:

```bash
# the project's SMTP login is shown under "Development info" in the UI (username = project slug)
swaks --to anyone@example.com --from app@example.com --server localhost:1025 \
      --auth-user demo --auth-password '<password from the UI>' --header "Subject: hello"
```

Without TLS files the server generates a self-signed certificate for `MAILDEV_HOSTNAME`. Put `tls.crt`/`tls.key` into `./certs` and set `MAILDEV_TLS_CERT=/certs/tls.crt`, `MAILDEV_TLS_KEY=/certs/tls.key` to use your own. Switch to Active Directory with `MAILDEV_AUTH_MODE=ldap` and the `MAILDEV_LDAP_*` variables, or to password-only accounts with `MAILDEV_AUTH_MODE=local` (then create accounts from the admin page; the first admin comes from `MAILDEV_ADMIN_USERS` only in `dev`/`ldap` modes, so for `local` start once in `dev` mode or use the lite edition's bootstrap).

Stop with `docker compose down`; the PostgreSQL volume `pgdata` keeps the data.

## Repository layout

```
backend/    Go service: SMTP, IMAP, POP3, REST API, provider adapters, PostgreSQL store, relay, S/MIME, encryption
frontend/   Next.js 16 / React 19 / Tailwind 4 web UI, built-in user guide (public/guide, generated by scripts/guide)
lite/       Lite edition: SQLite store + embedded static UI in one binary; reuses backend/internal verbatim
deploy/     Kubernetes (Kustomize) manifests, OpenBao Transit/AppRole setup, Makefile for image build/push/deploy
docs/       Architecture notes
docker-compose.yml, .env.example, Makefile   one-host deployment and developer shortcuts
```

## Configuration

All settings are environment variables prefixed `MAILDEV_`. The important ones (full list in [`backend/internal/config/config.go`](backend/internal/config/config.go)):

| Variable | Default | Meaning |
|---|---|---|
| `MAILDEV_MASTER_KEY` | required | base64 of 32 bytes; encrypts stored credentials and relay secrets |
| `MAILDEV_DATABASE_URL` | `postgres://maildev:maildev@localhost:5432/maildev?sslmode=disable` | PostgreSQL 14+ |
| `MAILDEV_AUTH_MODE` | `ldap` | `ldap`, `local` or `dev` |
| `MAILDEV_ADMIN_USERS` | | usernames promoted to admin at login (directory/dev users) |
| `MAILDEV_LDAP_URL`, `MAILDEV_LDAP_BASE_DN`, `MAILDEV_LDAP_UPN_SUFFIX`, `MAILDEV_LDAP_CA_FILE`, `MAILDEV_LDAP_BIND_DN`, `MAILDEV_LDAP_BIND_PASSWORD` | | LDAPS sign-in; the bind account is optional and only enables username lookup |
| `MAILDEV_DIRECTORY_URL` | | JSON list of company users (`username`, `fullname`, `email`, `department`) imported every 15 minutes for the member picker |
| `MAILDEV_HOSTNAME`, `MAILDEV_PUBLIC_URL`, `MAILDEV_MAIL_HOST` | `maildev.local`, `http://localhost:3000`, = hostname | names shown to users and used in SMTP/IMAP greetings and the self-signed certificate |
| `MAILDEV_HTTP_ADDR`, `MAILDEV_SMTP_ADDRS`, `MAILDEV_SMTPS_ADDRS`, `MAILDEV_IMAP_ADDRS`, `MAILDEV_IMAPS_ADDRS`, `MAILDEV_POP3_ADDRS`, `MAILDEV_POP3S_ADDRS` | `:8080`, `:1025`, `` , `:1143`, ``, `:1110`, `` | listeners (comma separated) |
| `MAILDEV_PUBLIC_*_PORTS` | | ports to display when a load balancer maps 25/587/465/143/993/110/995 onto the listeners |
| `MAILDEV_TLS_CERT`, `MAILDEV_TLS_KEY` | | certificate for SMTPS/STARTTLS/IMAPS/POP3S (reloaded on change); self-signed when empty |
| `MAILDEV_OPENBAO_ADDR`, `MAILDEV_OPENBAO_ROLE_ID`, `MAILDEV_OPENBAO_SECRET_ID` (or `MAILDEV_OPENBAO_TOKEN`), `MAILDEV_OPENBAO_TRANSIT_MOUNT` | | enables at-rest encryption via Transit |
| `MAILDEV_SMIME_CA_URLS`, `MAILDEV_SMIME_CA_FILE`, `MAILDEV_SMIME_SYSTEM_ROOTS` | , , `true` | S/MIME trust anchors |
| `MAILDEV_MAX_MESSAGE_BYTES`, `MAILDEV_SESSION_TTL`, `MAILDEV_COOKIE_SECURE` | 25 MB, `12h`, `true` | limits; set `MAILDEV_COOKIE_SECURE=false` when the UI is served over plain HTTP |
| `MAILDEV_DEMO_PROJECT` | `true` | seed the auto-join `demo` project |
| `MAILDEV_EDITION` | `full` | `lite` switches the single-project rules (see `lite/`) |

## Sending mail to MailDev

Every project has three credentials (shown under *Development info*, rotated with *Re-initialize*):

| Credential | Used by |
|---|---|
| SMTP/IMAP/POP3 login (`username` = project slug) | any SMTP library, Outlook, Thunderbird |
| API key `<key id>.<secret>` | `POST /api/v1/send` (JSON or raw RFC 822), SendGrid `POST /sendgrid/v3/mail/send`, Azure `POST /azure/<key id>/emails:send`, Graph `POST /graph/<key id>/v1.0/users/<any>/sendMail`, Gmail `POST /gmail/<key id>/gmail/v1/users/<any>/messages/send` |
| SES access key id + secret | `POST /ses` (v1 query API) and `POST /ses/v2/email/outbound-emails`; the key id selects the project, SigV4 is not verified |

The UI's *Development info* page shows ready-to-paste snippets for Node.js, Python, Java, .NET, PHP, Go and curl per provider.

## Development

### Prerequisites

- Go 1.26+, Node.js 24+, PostgreSQL 14+ (or the `postgres` service from the compose file), Python 3 for the end-to-end scripts (standard library only).

### Backend

```bash
cd backend
go build ./... && go vet ./...
go test ./...
export MAILDEV_AUTH_MODE=dev MAILDEV_ADMIN_USERS=admin MAILDEV_COOKIE_SECURE=false \
       MAILDEV_MASTER_KEY=$(openssl rand -base64 32) \
       MAILDEV_DATABASE_URL=postgres://maildev:maildev@localhost:5432/maildev?sslmode=disable
go run ./cmd/maildev          # HTTP :8080, SMTP :1025, IMAP :1143, POP3 :1110; schema migrates itself
```

Migrations live in `backend/internal/db/migrations/NNNN_name.sql`, applied in order at startup and tracked in `schema_migrations`. Add a new file, never edit an applied one.

### Frontend

```bash
cd frontend
npm install
npm run dev                   # http://localhost:3000, proxies /api, /sendgrid, /ses, /azure, /graph, /gmail to API_UPSTREAM (default http://localhost:8080)
npm run typecheck && npm run build
```

Every UI string goes through `useT().t("key")`; add new keys to all three locales in `lib/translations.ts` (`en`, `vi`, `ja`). `lib/api.ts` is the typed fetch wrapper, `lib/auth.tsx` the session context, `components/Shell.tsx` the authenticated layout, project pages share `components/ProjectNav.tsx` and `lib/project.tsx`.

### End-to-end tests

The Python scripts drive a real backend over SMTP, IMAP, POP3 and HTTP and print `PASS`/`FAIL` per check:

```bash
export MAILDEV_AUTH_MODE=dev MAILDEV_ADMIN_USERS=admin MAILDEV_COOKIE_SECURE=false MAILDEV_HOSTNAME=localhost \
  MAILDEV_HTTP_ADDR=:18080 MAILDEV_SMTP_ADDRS=:11025 MAILDEV_SMTPS_ADDRS=:11465 MAILDEV_IMAP_ADDRS=:11143 MAILDEV_IMAPS_ADDRS=:11993 \
  MAILDEV_POP3_ADDRS=:11110 MAILDEV_POP3S_ADDRS=:11995 MAILDEV_SMIME_SYSTEM_ROOTS=false \
  MAILDEV_MASTER_KEY=$(openssl rand -base64 32) MAILDEV_DATABASE_URL=postgres://maildev:maildev@localhost:5432/maildev_test?sslmode=disable
(cd backend && go run ./cmd/maildev &)
python3 -I backend/scripts/smoke_test.py        # SMTP/API ingestion, search, RBAC, audit, IMAP/POP3, SSE, retention
python3 -I backend/scripts/relay_test.py        # relay through SMTP, SES and SendGrid targets (served by the backend itself)
python3 -I backend/scripts/local_users_test.py  # password accounts, lockout, archive semantics
# optional: smime_test.py (generates a test PKI), encryption_test.py (needs an OpenBao dev server, see deploy/openbao/setup.sh)
```

Use a fresh database; the scripts assume the `demo` project and create `erp-test`.

### Lite edition

`lite/` holds a second Go module with the same import path. Everything under `lite/internal` except `store` (SQLite) and `webui` (embedded static export) is a verbatim copy of `backend/internal`, refreshed with `lite/scripts/sync-from-full.sh`; the UI is built from `frontend/` with `NEXT_OUTPUT=export`.

```bash
make lite-image                 # docker build -f lite/Dockerfile .
cd lite && make test            # runs the backend's end-to-end suites against the lite binary + lite_test.py
cd lite && make web build && ./dist/maildev-lite     # https://localhost:8443, admin/admin
```

Packaging for an offline site, certificates and operations are described in [`lite/README.md`](lite/README.md).

### Built-in user guide

`frontend/public/guide/` is generated: `frontend/scripts/guide/gen_guide.py` and `gen_lite_guide.py` build the HTML (three languages), `shoot.mjs` / `shoot-lite.mjs` take the annotated screenshots with Playwright against a running instance (`GUIDE_BASE=http://localhost:3000 node shoot.mjs <outdir> <user> <password> <locale>`). Regenerate after UI changes instead of editing the HTML.

### Conventions

- One PR per repository area; keep `backend` and `lite` in sync (run the sync script and `cd lite && make test` after touching `backend/internal`).
- Every captured message goes through `internal/ingest`; every store write that should be visible to users emits an audit entry via `store.Audit` (never fails the caller).
- Handlers must not touch the database driver directly: add a store method (the lite edition re-implements the store API for SQLite).
- New provider adapters need a route prefix in `frontend/next.config.ts` (rewrites), `deploy/k8s/httproute.yaml` and `lite/cmd/maildev-lite/main.go` (`route`).

## Deploying to Kubernetes

`deploy/k8s` is a Kustomize base (API deployment with one replica because the event bus is in-process, web deployment, `HTTPRoute` for Gateway API, `LoadBalancer` service for the mail ports, cert-manager `Certificate`, ConfigMap). `deploy/Makefile` builds and pushes the two images and applies the manifests with a tag; `deploy/openbao/setup.sh` creates the Transit mount, policy and AppRole for at-rest encryption; `deploy/k8s/make-secrets.sh` creates the secrets from environment variables. Edit the hostnames, registry and ConfigMap for your environment; see [`deploy/README.md`](deploy/README.md).

## Security notes

- Nothing is ever relayed automatically. Relay is per project, admin-configured, owner-approved and fully audited.
- Project credentials are shown in clear in the UI on purpose (they only reach this catcher); they are stored AES-GCM encrypted with the master key.
- HTML preview is sanitized (bluemonday) and rendered in a `sandbox=""` iframe with a CSP that blocks remote resources unless the user asks for them.
- Run behind HTTPS (`MAILDEV_COOKIE_SECURE=true`) and restrict the SMTP/IMAP/POP3 ports to the networks that need them.

## License

MIT. Copyright (c) Bui The Phong <buithephong@gmail.com>.
