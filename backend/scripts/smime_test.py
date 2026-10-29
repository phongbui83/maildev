# S/MIME end-to-end test (needs openssl on PATH).
#   python3 -I backend/scripts/smime_test.py gen <dir>   # creates test CA + signer certs + sample messages
#   ... start the backend with MAILDEV_SMIME_CA_FILE=<dir>/ca.crt (dev auth, ports as in smoke_test.py)
#   python3 -I backend/scripts/smime_test.py run <dir>   # sends the samples and checks verification results
import json, os, smtplib, subprocess, sys, time, urllib.request, http.cookiejar

API = "http://localhost:18080"


def sh(*args):
    subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def gen(d):
    os.makedirs(d, exist_ok=True)
    os.chdir(d)
    sh("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.crt", "-days", "30", "-subj", "/CN=MailDev Test CA")
    sh("openssl", "req", "-newkey", "rsa:2048", "-nodes", "-keyout", "user.key", "-out", "user.csr", "-subj", "/CN=Test Signer/emailAddress=signer@example.com")
    open("ext.cnf", "w").write("subjectAltName=email:signer@example.com\nextendedKeyUsage=emailProtection\nkeyUsage=digitalSignature\n")
    sh("openssl", "x509", "-req", "-in", "user.csr", "-CA", "ca.crt", "-CAkey", "ca.key", "-CAcreateserial", "-out", "user.crt", "-days", "30", "-extfile", "ext.cnf")
    sh("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "other.key", "-out", "other.crt", "-days", "30", "-subj", "/CN=Untrusted Signer/emailAddress=evil@example.com", "-addext", "subjectAltName=email:evil@example.com")
    open("body.txt", "wb").write(b"Content-Type: text/plain; charset=utf-8\r\n\r\nHello signed world\r\n")
    common = ["-from", "signer@example.com", "-to", "c@d.test"]
    sh("openssl", "smime", "-sign", "-in", "body.txt", "-signer", "user.crt", "-inkey", "user.key", "-out", "signed.eml", "-subject", "SMIME detached", *common)
    sh("openssl", "smime", "-sign", "-nodetach", "-in", "body.txt", "-signer", "user.crt", "-inkey", "user.key", "-out", "opaque.eml", "-subject", "SMIME opaque", *common)
    sh("openssl", "smime", "-sign", "-in", "body.txt", "-signer", "other.crt", "-inkey", "other.key", "-out", "untrusted.eml", "-subject", "SMIME untrusted", "-from", "evil@example.com", "-to", "c@d.test")
    sh("openssl", "smime", "-encrypt", "-in", "body.txt", "-from", "a@b.test", "-to", "c@d.test", "-subject", "SMIME encrypted", "-out", "encrypted.eml", "user.crt")
    print("generated in", d)


def run(d):
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))

    def call(m, p, b=None):
        req = urllib.request.Request(API + p, data=None if b is None else json.dumps(b).encode(), method=m, headers={"Content-Type": "application/json"})
        try:
            r = op.open(req)
            x = r.read()
            return r.status, (json.loads(x) if x else None)
        except urllib.error.HTTPError as e:
            x = e.read()
            return e.code, (json.loads(x) if x else None)

    def check(c, m):
        print(("PASS " if c else "FAIL ") + m)
        if not c:
            sys.exit(1)

    call("POST", "/api/auth/login", {"username": "admin", "password": "dev"})
    st, projs = call("GET", "/api/projects")
    demo = [p for p in projs if p["slug"] == "demo"][0]
    smtp = [c for c in call("GET", f"/api/projects/{demo['id']}/credentials")[1] if c["kind"] == "smtp"][0]
    s = smtplib.SMTP("localhost", 11025, timeout=10)
    s.login(smtp["username"], smtp["secret"])
    for f in ("signed.eml", "opaque.eml", "untrusted.eml", "encrypted.eml"):
        raw = open(os.path.join(d, f), "rb").read()
        s.sendmail("sender@example.com", ["c@d.test"], raw)
    s.quit()
    time.sleep(0.5)
    st, lst = call("GET", f"/api/projects/{demo['id']}/emails?q=SMIME")
    by = {e["subject"]: e for e in lst["items"]}
    e = by["SMIME detached"]
    check(e["smime"] == "signed" and e.get("smime_valid") is True and "signer@example.com" in e.get("smime_signer", ""), f"detached signature valid, signer {e.get('smime_signer')}")
    full = call("GET", f"/api/projects/{demo['id']}/emails/{e['id']}")[1]
    check("Hello signed world" in full["text_body"] and any(a["filename"] == "smime.p7s" for a in full["attachments"]), "detached: body readable, p7s kept as attachment")
    e = by["SMIME opaque"]
    check(e["smime"] == "signed-opaque" and e.get("smime_valid") is True, "opaque signature valid")
    full = call("GET", f"/api/projects/{demo['id']}/emails/{e['id']}")[1]
    check("Hello signed world" in full["text_body"], "opaque: inner content extracted for display")
    e = by["SMIME untrusted"]
    check(e["smime"] == "signed" and e.get("smime_valid") is False and e.get("smime_error") and "evil@example.com" in e.get("smime_signer", ""), f"untrusted CA rejected: {e.get('smime_error')[:60]}")
    e = by["SMIME encrypted"]
    check(e["smime"] == "encrypted" and not e.get("smime_signer"), "encrypted message tagged, not decrypted")
    print("ALL PASS")


if __name__ == "__main__":
    if len(sys.argv) != 3 or sys.argv[1] not in ("gen", "run"):
        print(__doc__)
        sys.exit(2)
    (gen if sys.argv[1] == "gen" else run)(os.path.abspath(sys.argv[2]))
