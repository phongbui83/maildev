# Relay feature test. Run AFTER smoke_test.py against the same dev backend (it
# relies on the "erp-test" project and the users admin/alice/bob existing).
# Project "erp-test" relays into project "relay-target" on the same instance via
# SMTP, SES-compatible and SendGrid-compatible targets, so nothing leaves the box.
# Usage: python3 -I backend/scripts/relay_test.py
import json, smtplib, sys, time, urllib.request, http.cookiejar
from email.message import EmailMessage

API = "http://localhost:18080"
cj = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))


def call(method, path, body=None, headers=None):
    h = {"Content-Type": "application/json"}
    if headers:
        h.update(headers)
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(API + path, data=data, method=method, headers=h)
    try:
        r = op.open(req)
        b = r.read()
        return r.status, (json.loads(b) if b else None)
    except urllib.error.HTTPError as e:
        b = e.read()
        return e.code, (json.loads(b) if b else None)


def check(cond, msg):
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        sys.exit(1)


def login(u):
    cj.clear()
    st, _ = call("POST", "/api/auth/login", {"username": u, "password": "dev"})
    check(st == 200, f"login {u}")


login("admin")
st, projs = call("GET", "/api/projects")
A = [p for p in projs if p["slug"] == "erp-test"][0]
st, B = call("POST", "/api/projects", {"slug": "relay-target", "name": "Relay target", "init": True, "owner_username": "alice"})
check(st == 201, "create relay-target project")
st, bcreds = call("GET", f"/api/projects/{B['id']}/credentials")
bsmtp = [c for c in bcreds if c["kind"] == "smtp"][0]
bapi = [c for c in bcreds if c["kind"] == "api"][0]
bses = [c for c in bcreds if c["kind"] == "ses"][0]
st, acreds = call("GET", f"/api/projects/{A['id']}/credentials")
asmtp = [c for c in acreds if c["kind"] == "smtp"][0]


def send_to_A(subject, to="customer@gmail.com"):
    msg = EmailMessage()
    msg["From"] = "App <noreply@erp.example>"
    msg["To"] = to
    msg["Subject"] = subject
    msg.set_content("relay me")
    msg.add_alternative("<b>relay me</b>", subtype="html")
    s = smtplib.SMTP("localhost", 11025, timeout=10)
    s.login(asmtp["username"], asmtp["secret"])
    s.send_message(msg)
    s.quit()


def find_in(project, subject, prefix=False):
    st, lst = call("GET", f"/api/projects/{project['id']}/emails?q={urllib.request.quote(subject)}")
    return [e for e in lst["items"] if (e["subject"].startswith(subject) if prefix else e["subject"] == subject)]


# --- enabling requires config; config is admin-only ---
st, r = call("PUT", f"/api/projects/{A['id']}/relay", {"enabled": True})
check(st == 400, "cannot enable relay without a target")
cfg = {"kind": "smtp", "host": "localhost", "port": 11025, "security": "none", "username": bsmtp["username"], "secret": bsmtp["secret"], "enabled": True}
st, r = call("PUT", f"/api/projects/{A['id']}/relay", cfg)
check(st == 200 and r["enabled"] and r["config"]["has_secret"] and "secret" not in r["config"], f"admin configures smtp relay and enables -> {st}")
st, r = call("POST", f"/api/projects/{A['id']}/relay/test")
check(st == 200 and r["ok"], f"relay test -> {r}")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", {"kind": "smtp", "host": "localhost", "port": 11025, "security": "none", "username": bsmtp["username"], "secret": "wrong", "enabled": True})
st, r = call("POST", f"/api/projects/{A['id']}/relay/test")
check(st == 200 and not r["ok"], f"relay test with wrong password fails -> {r['message'][:50]}")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", cfg)

# captured mail becomes pending automatically
send_to_A("Relay one")
time.sleep(0.3)
e1 = find_in(A, "Relay one")[0]
check(e1["relay_status"] == "pending", f"new mail is pending approval ({e1['relay_status']})")
check(not find_in(B, "Relay one"), "not delivered before approval")

# --- RBAC ---
login("bob")  # member
st, r = call("POST", f"/api/projects/{A['id']}/emails/{e1['id']}/relay/approve")
check(st == 403, "member cannot approve")
st, r = call("GET", f"/api/projects/{A['id']}/relay/queue")
check(st == 403, "member cannot see the queue")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", cfg)
check(st == 403, "member cannot configure relay")
login("alice")  # owner
st, r = call("PUT", f"/api/projects/{A['id']}/relay", cfg)
check(st == 403, "owner cannot configure relay (admin only)")
st, r = call("GET", f"/api/projects/{A['id']}/relay")
check(st == 200 and r["enabled"] and r["config"]["kind"] == "smtp", "owner can view relay status")
st, r = call("GET", f"/api/projects/{A['id']}/relay/queue?status=pending")
check(st == 200 and r["total"] == 1, "owner sees 1 pending")

# --- approve single ---
st, r = call("POST", f"/api/projects/{A['id']}/emails/{e1['id']}/relay/approve", {"note": "ok to send"})
check(st == 200 and r["results"][0]["status"] == "sent", f"owner approves single -> {r}")
time.sleep(0.3)
got = find_in(B, "Relay one")
check(len(got) == 1 and got[0]["source"] == "smtp" and got[0]["envelope_to"] == ["customer@gmail.com"], "delivered to relay target via SMTP")
st, e = call("GET", f"/api/projects/{A['id']}/emails/{e1['id']}")
check(e["relay_status"] == "sent" and e["relay_decided_by"] == "alice" and e["relay_note"] == "ok to send", "relay fields recorded")
st, r = call("POST", f"/api/projects/{A['id']}/emails/{e1['id']}/relay/approve")
check(r["results"][0]["status"] == "already_sent", "double approve is a no-op")

# --- batch approve / reject ---
for i in range(3):
    send_to_A(f"Relay batch {i}")
send_to_A("Relay reject me")
time.sleep(0.5)
st, q = call("GET", f"/api/projects/{A['id']}/relay/queue?status=pending")
check(q["total"] == 4, f"4 pending ({q['total']})")
rej = [e for e in q["items"] if e["subject"] == "Relay reject me"][0]
st, r = call("POST", f"/api/projects/{A['id']}/relay/reject", {"ids": [rej["id"]], "note": "test data"})
check(st == 200 and r["results"][0]["status"] == "rejected", "batch reject by ids")
st, r = call("POST", f"/api/projects/{A['id']}/relay/approve", {"all": True})
check(st == 200 and len(r["results"]) == 3 and all(x["status"] == "sent" for x in r["results"]), f"approve all pending -> {[x['status'] for x in r['results']]}")
time.sleep(0.3)
check(len(find_in(B, "Relay batch", prefix=True)) == 3, "3 batch mails delivered")
st, q = call("GET", f"/api/projects/{A['id']}/relay/queue?status=pending")
check(q["total"] == 0, "queue empty")
st, q = call("GET", f"/api/projects/{A['id']}/relay/queue")
check(q["total"] == 5, f"history keeps 5 decided messages ({q['total']})")

# --- recipient allow-list (admin) ---
login("admin")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", {**cfg, "secret": "", "allowed_recipients": "*@example.com, qa-*@example.com"})
check(st == 200 and r["config"]["allowed_recipients"].startswith("*@example"), "allow-list saved, secret kept")
send_to_A("Relay blocked", to="someone@gmail.com")
send_to_A("Relay allowed", to="qa-1@example.com")
time.sleep(0.4)
login("alice")
blocked = find_in(A, "Relay blocked")[0]
allowed = find_in(A, "Relay allowed")[0]
st, r = call("POST", f"/api/projects/{A['id']}/relay/approve", {"ids": [blocked["id"], allowed["id"]]})
res = {x["id"]: x for x in r["results"]}
check(res[blocked["id"]]["status"] == "failed" and "not allowed" in res[blocked["id"]]["error"], "recipient outside allow-list is refused")
check(res[allowed["id"]]["status"] == "sent", "allowed recipient is sent")
st, r = call("POST", f"/api/projects/{A['id']}/emails/{blocked['id']}/relay/request")
check(st == 204, "failed message can be re-requested")

# --- SES-compatible relay (into our own SES endpoint) ---
login("admin")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", {"kind": "ses", "region": "ap-southeast-1", "endpoint": API + "/ses", "access_key_id": bses["username"], "secret": bses["secret"], "allowed_recipients": ""})
check(st == 200 and r["config"]["kind"] == "ses", "switch relay to SES")
send_to_A("Relay via SES")
time.sleep(0.3)
login("alice")
e = find_in(A, "Relay via SES")[0]
st, r = call("POST", f"/api/projects/{A['id']}/emails/{e['id']}/relay/approve")
check(r["results"][0]["status"] == "sent" and r["results"][0]["remote_id"], f"SES relay sent -> {r['results'][0]}")
time.sleep(0.3)
got = find_in(B, "Relay via SES")
check(len(got) == 1 and got[0]["source"] == "ses", "delivered through SES v2 raw API (SigV4 signed)")

# --- SendGrid-compatible relay ---
login("admin")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", {"kind": "sendgrid", "endpoint": API + "/sendgrid", "secret": bapi["username"] + "." + bapi["secret"], "from_override": "Verified Sender <verified@example.com>"})
check(st == 200 and r["config"]["kind"] == "sendgrid", "switch relay to SendGrid with from override")
send_to_A("Relay via SendGrid")
time.sleep(0.3)
login("alice")
e = find_in(A, "Relay via SendGrid")[0]
st, r = call("POST", f"/api/projects/{A['id']}/emails/{e['id']}/relay/approve")
check(r["results"][0]["status"] == "sent", f"SendGrid relay sent -> {r['results'][0]}")
time.sleep(0.3)
got = find_in(B, "Relay via SendGrid")
check(len(got) == 1 and got[0]["source"] == "sendgrid" and got[0]["from"][0]["address"] == "verified@example.com", "delivered through SendGrid API with overridden sender")

# --- disable ---
login("admin")
st, r = call("PUT", f"/api/projects/{A['id']}/relay", {"enabled": False})
check(st == 200 and not r["enabled"], "admin disables relay")
send_to_A("Relay off")
time.sleep(0.3)
e = find_in(A, "Relay off")[0]
check(e["relay_status"] == "none", "new mail no longer queued")
login("alice")
st, r = call("POST", f"/api/projects/{A['id']}/emails/{e['id']}/relay/request")
check(st == 400, "request refused when relay disabled")

# --- audit (global log is auditor-only; carol was promoted by smoke_test.py) ---
login("carol")
st, a = call("GET", "/api/admin/audit?limit=500")
actions = {x["action"] for x in a["items"]}
need = {"relay_config_update", "relay_enable", "relay_disable", "relay_test", "relay_approve", "relay_sent", "relay_failed", "relay_reject", "relay_request"}
check(need <= actions, f"audit covers all relay actions (missing: {need - actions})")
sent = [x for x in a["items"] if x["action"] == "relay_sent"]
check(all(x["actor"] == "alice" and x["detail"].get("recipients") for x in sent), "relay_sent entries carry actor and recipients")
print("ALL PASS")
