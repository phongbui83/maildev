# At-rest encryption test. Needs a backend started with MAILDEV_OPENBAO_* set (see
# deploy/openbao/setup.sh against a dev OpenBao) and direct DB access via psql.
# Usage: python3 -I backend/scripts/encryption_test.py [psql args...]
import json, smtplib, subprocess, sys, time, urllib.request, http.cookiejar, imaplib, poplib
from email.message import EmailMessage

API = "http://localhost:18080"
PSQL = sys.argv[1:] or ["-h", "localhost", "-U", "maildev", "-d", "maildev_dev"]
cj = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))


def call(m, p, b=None):
    req = urllib.request.Request(API + p, data=None if b is None else json.dumps(b).encode(), method=m, headers={"Content-Type": "application/json"})
    r = op.open(req)
    x = r.read()
    return r.status, (json.loads(x) if x else None)


def check(c, m):
    print(("PASS " if c else "FAIL ") + m)
    if not c:
        sys.exit(1)


def sql(q):
    return subprocess.run(["psql", *PSQL, "-tAc", q], capture_output=True, text=True, check=True).stdout.strip()


st, cfg = call("GET", "/api/config")
check(cfg["encryption"], "config reports encryption enabled")
call("POST", "/api/auth/login", {"username": "admin", "password": "dev"})
demo = [p for p in call("GET", "/api/projects")[1] if p["slug"] == "demo"][0]
smtp = [c for c in call("GET", f"/api/projects/{demo['id']}/credentials")[1] if c["kind"] == "smtp"][0]
secret = "TOPSECRET" + str(int(time.time()))
msg = EmailMessage()
msg["From"] = "a@b.test"
msg["To"] = "c@d.test"
msg["Subject"] = "Encrypted body test"
msg.set_content(f"body contains {secret}")
msg.add_attachment(f"attachment contains {secret}".encode(), maintype="text", subtype="plain", filename="s.txt")
s = smtplib.SMTP("localhost", 11025, timeout=10)
s.login(smtp["username"], smtp["secret"])
s.send_message(msg)
s.quit()
time.sleep(0.5)
e = [x for x in call("GET", f"/api/projects/{demo['id']}/emails?q=Encrypted")[1]["items"] if x["subject"] == "Encrypted body test"][0]
row = sql(f"SELECT encrypted, length(text_body), length(html_body), length(body_enc) > 0, position('{secret}' in encode(raw,'escape')) FROM emails WHERE id='{e['id']}'").split("|")
check(row[0] == "t" and row[1] == "0" and row[3] == "t" and row[4] == "0", f"DB row is encrypted (no plaintext body): {row}")
arow = sql(f"SELECT encrypted, position('{secret}' in encode(data,'escape')) FROM attachments WHERE email_id='{e['id']}'")
check(arow == "t|0", f"attachment encrypted in DB: {arow}")
wrapped = sql(f"SELECT dek_wrapped FROM projects WHERE id='{demo['id']}'")
check(wrapped.startswith("vault:v1:"), f"project data key wrapped by Transit ({wrapped[:12]}...)")
full = call("GET", f"/api/projects/{demo['id']}/emails/{e['id']}")[1]
check(secret in full["text_body"], "API decrypts body transparently")
att = full["attachments"][0]
data = op.open(API + f"/api/projects/{demo['id']}/emails/{e['id']}/attachments/{att['id']}").read()
check(secret.encode() in data, "API decrypts attachment")
raw = op.open(API + f"/api/projects/{demo['id']}/emails/{e['id']}/raw").read()
check(secret.encode() in raw, "raw download decrypted")
M = imaplib.IMAP4("localhost", 11143)
M.login(smtp["username"], smtp["secret"])
M.select("INBOX")
typ, d = M.uid("fetch", str(e["uid"]), "(BODY.PEEK[])")
check(secret.encode() in d[0][1], "IMAP fetch decrypted")
M.logout()
P = poplib.POP3("localhost", 11110)
P.user(smtp["username"])
P.pass_(smtp["secret"])
n, _ = P.stat()
resp, lines, _ = P.retr(n)
check(any(secret.encode() in l for l in lines), "POP3 RETR decrypted")
P.quit()
check(sql("SELECT count(*) FROM emails WHERE NOT encrypted") == "0", "backlog: no unencrypted messages remain")
check(sql("SELECT count(*) FROM attachments WHERE NOT encrypted") == "0", "backlog: no unencrypted attachments remain")
check(sql("SELECT count(*) FROM emails WHERE position('Hello plain text' in encode(raw,'escape')) > 0") == "0", "earlier plaintext bodies are gone from the DB")
print("ALL PASS")
