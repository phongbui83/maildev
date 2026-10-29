# Lite-specific end-to-end checks. Run against `maildev-lite` started with its
# defaults (local auth, single project) on the test ports (see scripts/test.sh).
# Usage: python3 -I scripts/lite_test.py
import json, smtplib, sys, urllib.request, http.cookiejar, time
from email.message import EmailMessage

API = "http://localhost:18080"

def client():
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
    def call(method, path, body=None, headers=None, raw=False):
        data = None
        h = {"Content-Type": "application/json"}
        if headers: h.update(headers)
        if body is not None:
            data = body if isinstance(body, bytes) else json.dumps(body).encode()
        req = urllib.request.Request(API + path, data=data, method=method, headers=h)
        try:
            r = op.open(req); b = r.read()
            return r.status, (b if raw else (json.loads(b) if b else None))
        except urllib.error.HTTPError as e:
            b = e.read()
            return e.code, (b if raw else (json.loads(b) if b else None))
    return call

def check(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond: sys.exit(1)

admin = client()
st, cfg = admin("GET", "/api/config"); check(st == 200 and cfg["edition"] == "lite" and cfg["auth_mode"] == "local", f"config edition={cfg.get('edition')} auth={cfg.get('auth_mode')}")
st, html = admin("GET", "/", raw=True); check(st == 200 and b"<html" in html.lower(), "web UI served at /")
st, html = admin("GET", "/projects/whatever/inbox", raw=True); check(st in (200, 404), f"project page resolution -> {st}")

st, me = admin("POST", "/api/auth/login", {"username": "admin", "password": "admin"})
check(st == 200 and me["role"] == "admin" and me["must_change_password"], "admin/admin login, password change required")
st, projs = admin("GET", "/api/projects"); check(st == 200 and [p["slug"] for p in projs] == ["default"] and projs[0]["my_role"] == "owner", f"single project {[(p['slug'], p['my_role']) for p in projs]}")
pid = projs[0]["id"]
st, r = admin("POST", "/api/projects", {"slug": "second", "name": "Second"}); check(st == 403, "creating a second project is refused")
st, r = admin("POST", "/api/auth/password", {"current_password": "admin", "new_password": "Correct-Horse-9!"}); check(st == 204, "admin changes password")
admin = client()
st, me = admin("POST", "/api/auth/login", {"username": "admin", "password": "admin"}); check(st == 401, "old password rejected")
st, me = admin("POST", "/api/auth/login", {"username": "admin", "password": "Correct-Horse-9!"}); check(st == 200 and not me["must_change_password"], "new password accepted, flag cleared")

st, creds = admin("GET", f"/api/projects/{pid}/credentials"); check(st == 200 and len(creds) == 3, f"credentials issued: {sorted(c['kind'] for c in creds)}")
smtp = [c for c in creds if c["kind"] == "smtp"][0]

st, r = admin("POST", "/api/admin/local-users", {"username": "bob", "display_name": "Bob", "password": "Bob-Secret-2026!"}); check(st == 201, "admin creates account bob")
bob_id = r["user"]["id"]
st, r = admin("PATCH", f"/api/admin/users/{bob_id}", {"role": "auditor"}); check(st == 200 and r["role"] == "auditor", "local account may be auditor in lite")
st, r = admin("PATCH", f"/api/admin/users/{bob_id}", {"role": "user"}); check(st == 200, "back to user")

bob = client()
st, me = bob("POST", "/api/auth/login", {"username": "bob", "password": "Bob-Secret-2026!"}); check(st == 200 and me["must_change_password"], "bob logs in (must change password)")
st, projs = bob("GET", "/api/projects"); check(st == 200 and len(projs) == 1 and projs[0]["my_role"] == "member", "bob auto-joined the project as member")
st, r = admin("POST", f"/api/projects/{pid}/members", {"username": "bob", "role": "owner"}); check(st == 200, "local account can be made owner in lite")
st, projs = bob("GET", "/api/projects"); check(projs[0]["my_role"] == "owner", "bob is now owner")
st, r = bob("GET", "/api/admin/users"); check(st == 403, "bob (user role) cannot access admin")

m = EmailMessage(); m["From"] = "app@customer.local"; m["To"] = "someone@example.com"; m["Subject"] = "lite smoke"; m.set_content("hello from lite")
s = smtplib.SMTP("localhost", 11025); s.starttls(); s.login(smtp["username"], smtp["secret"]); s.send_message(m); s.quit()
time.sleep(0.5)
st, r = bob("GET", f"/api/projects/{pid}/emails?q=lite"); check(st == 200 and r["total"] == 1 and r["items"][0]["subject"] == "lite smoke", "captured mail visible to bob")
st, r = admin("GET", f"/api/projects/{pid}/audit?protocol=smtp"); check(st == 200 and r["total"] >= 2, f"audit records smtp login + capture ({r['total']})")
st, r = admin("GET", "/api/admin/audit"); check(st == 200 and any(x["action"] == "local_user_create" for x in r["items"]), "global log has bootstrap/user creation")
st, r = admin("PATCH", f"/api/projects/{pid}", {"retention_days": 30}); check(st == 200 and r["retention_days"] == 30, "admin sets retention")
print("ALL PASS")
