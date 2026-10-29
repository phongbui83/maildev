# Local (external) account test. Run AFTER smoke_test.py on the same dev backend.
# Usage: python3 -I backend/scripts/local_users_test.py
import json, sys, urllib.request, http.cookiejar

API = "http://localhost:18080"
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


def login(u, pw="dev"):
    cj.clear()
    return call("POST", "/api/auth/login", {"username": u, "password": pw})


login("admin")
st, r = call("POST", "/api/admin/local-users", {"username": "cust1", "display_name": "Customer One", "email": "c1@customer.example"})
check(st == 201 and r["user"]["auth_source"] == "local" and r["user"]["role"] == "user" and r["user"]["must_change_password"] and r.get("password"), "admin creates external account with generated password")
tmp = r["password"]
cust = r["user"]
st, r = call("POST", "/api/admin/local-users", {"username": "cust1"}); check(st == 409, "duplicate username rejected")
st, r = call("POST", "/api/admin/local-users", {"username": "cust2", "password": "short"}); check(st == 400, "weak password rejected")
st, r = call("PATCH", f"/api/admin/users/{cust['id']}", {"role": "admin"}); check(st == 400, "external account cannot become admin")
st, r = call("PATCH", f"/api/admin/users/{cust['id']}", {"role": "auditor"}); check(st == 400, "external account cannot become auditor")
A = [p for p in call("GET", "/api/projects")[1] if p["slug"] == "erp-test"][0]
st, r = call("POST", f"/api/projects/{A['id']}/members", {"username": "cust1", "role": "owner"}); check(st == 400, "external account cannot be owner")
st, r = call("POST", f"/api/projects/{A['id']}/members", {"username": "cust1", "role": "member"}); check(st == 200 and not r["pending"], "external account added as member immediately")
if call("GET", "/api/config")[1].get("directory"):
    st, r = call("POST", f"/api/projects/{A['id']}/members", {"username": "nobody-here", "role": "member"}); check(st == 404, "unknown username rejected")
st, r = call("POST", "/api/admin/local-users", {"username": "cust3"})
call("POST", f"/api/projects/{A['id']}/members", {"username": "cust3", "role": "member"})
# lockout
for i in range(5):
    st, r = login("cust1", "wrong-" + str(i))
check(st == 423, "account locked after 5 failures (423)")
st, r = login("cust1", tmp); check(st == 423, "correct password refused while locked")
login("admin"); st, r = call("PATCH", f"/api/admin/users/{cust['id']}", {"unlock": True}); check(st == 200, "admin unlocks")
st, me = login("cust1", tmp); check(st == 200 and me["must_change_password"], "external login works, must change password")
st, projs = call("GET", "/api/projects")
slugs = {p["slug"]: p["my_role"] for p in projs}
check("demo" not in slugs and slugs.get("erp-test") == "member", f"external user sees only assigned projects as member: {slugs}")
st, r = call("GET", "/api/admin/users"); check(st == 403, "external user has no admin access")
st, r = call("POST", "/api/auth/password", {"current_password": "nope", "new_password": "Str0ng-Password!"}); check(st == 401, "wrong current password rejected")
st, r = call("POST", "/api/auth/password", {"current_password": tmp, "new_password": "Str0ng-Password!"}); check(st == 204, "password changed, session kept")
st, me = call("GET", "/api/auth/me"); check(st == 200 and not me["must_change_password"], "flag cleared after change")
st, me = login("cust1", tmp); check(st == 401, "old password no longer works")
st, me = login("cust1", "Str0ng-Password!"); check(st == 200, "new password works")
st, r = call("POST", f"/api/projects/{A['id']}/init?reinit=1"); check(st == 403, "external member cannot rotate project credentials")
st, r = call("GET", f"/api/projects/{A['id']}/credentials"); check(st == 200, "external member reads project credentials")
login("admin"); st, r = call("POST", f"/api/admin/users/{cust['id']}/reset-password", {}); check(st == 200 and r.get("password"), "admin resets password")
st, me = login("cust1", "Str0ng-Password!"); check(st == 401, "old password invalidated by reset")
st, me = login("cust1", r["password"]); check(st == 200 and me["must_change_password"], "temporary password works and forces change")
login("admin")
# --- archive semantics ---
import smtplib
st, ap = call("POST", "/api/projects", {"slug": "arch-test", "name": "Archive test", "init": True, "owner_username": "alice"})
asmtp = [c for c in call("GET", f"/api/projects/{ap['id']}/credentials")[1] if c["kind"] == "smtp"][0]
s_ = smtplib.SMTP("localhost", 11025, timeout=10); s_.login(asmtp["username"], asmtp["secret"]); s_.sendmail("a@b.test", ["c@d.test"], "Subject: before archive\r\n\r\nx"); s_.quit()
st, r = call("PATCH", f"/api/projects/{ap['id']}", {"status": "archived"}); check(st == 200 and r["status"] == "archived", "admin archives project")
try:
    s_ = smtplib.SMTP("localhost", 11025, timeout=10); s_.login(asmtp["username"], asmtp["secret"]); check(False, "smtp refused on archived project")
except smtplib.SMTPAuthenticationError:
    check(True, "smtp refused on archived project")
login("alice")
st, lst = call("GET", f"/api/projects/{ap['id']}/emails"); check(st == 200 and lst["total"] == 1, "owner still reads archived inbox")
st, r = call("DELETE", f"/api/projects/{ap['id']}/emails/{lst['items'][0]['id']}"); check(st == 403, "archived inbox is read-only")
st, r = call("POST", f"/api/projects/{ap['id']}/init?reinit=1"); check(st == 403, "no credential rotation on archived project")
login("admin")
st, r = call("PATCH", f"/api/projects/{ap['id']}", {"status": "active"}); check(st == 200 and r["status"] == "active", "admin reactivates project")
st, r = call("DELETE", f"/api/projects/{ap['id']}"); check(st == 204, "admin deletes project")
cj.clear(); call("POST", "/api/auth/login", {"username": "carol", "password": "dev"})
st, a = call("GET", "/api/admin/audit?limit=300")
acts = {x["action"] for x in a["items"]}
need = {"local_user_create", "password_change", "password_reset", "user_unlock"}
check(need <= acts, f"audit covers local account actions (missing {need - acts})")
print("ALL PASS")
