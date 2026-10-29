# MailDev backend

Go service: SMTP (25/587/465), IMAP (143/993), POP3 (110/995), REST API, SendGrid/SES-compatible ingestion, approved relay, S/MIME verification, OpenBao Transit at-rest encryption, AD (LDAPS) and local accounts.

```bash
go build ./... && go vet ./... && go test ./...
go run ./cmd/maildev        # see ../deploy/.env.example for configuration
```

End-to-end test scripts live in `scripts/`; deployment, docs and the full architecture notes are in the `deploy` repository (`CLAUDE.md`, `README.md`).
