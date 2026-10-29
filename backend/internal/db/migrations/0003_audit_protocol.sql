-- Record which channel an audited action came through (web, smtp, imap, pop3, api, sendgrid, ses, system).
ALTER TABLE audit_log ADD COLUMN protocol text NOT NULL DEFAULT 'web';
CREATE INDEX audit_log_protocol_idx ON audit_log(protocol, at DESC);

-- S/MIME detection: '' (none), 'signed' (multipart/signed), 'signed-opaque' (pkcs7 signed-data), 'encrypted' (pkcs7 enveloped-data).
ALTER TABLE emails ADD COLUMN smime text NOT NULL DEFAULT '';
ALTER TABLE emails
    ADD COLUMN smime_valid  boolean,
    ADD COLUMN smime_signer text NOT NULL DEFAULT '',
    ADD COLUMN smime_error  text NOT NULL DEFAULT '';
