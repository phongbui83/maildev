# End-to-end smoke test. Run against a backend started with MAILDEV_AUTH_MODE=dev,
# MAILDEV_ADMIN_USERS=admin and the ports below (see run instructions in CLAUDE.md).
# Usage: python3 -I backend/scripts/smoke_test.py
import json, smtplib, ssl, imaplib, base64, sys, urllib.request, http.cookiejar, time
from email.message import EmailMessage

API="http://localhost:18080"
cj=http.cookiejar.CookieJar()
op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
def call(method,path,body=None,headers=None,raw=False):
    data=None
    h={"Content-Type":"application/json"}
    if headers: h.update(headers)
    if body is not None:
        data=body if isinstance(body,bytes) else json.dumps(body).encode()
    req=urllib.request.Request(API+path,data=data,method=method,headers=h)
    try:
        r=op.open(req); b=r.read()
        return r.status,(b if raw else (json.loads(b) if b else None)),dict(r.headers)
    except urllib.error.HTTPError as e:
        b=e.read()
        return e.code,(b if raw else (json.loads(b) if b else None)),dict(e.headers)

def check(cond,msg):
    print(("PASS " if cond else "FAIL ")+msg)
    if not cond: sys.exit(1)

# 1. login admin
st,me,_=call("POST","/api/auth/login",{"username":"admin","password":"dev"})
check(st==200 and me["role"]=="admin", f"admin login -> role {me and me.get('role')}")
st,projs,_=call("GET","/api/projects"); check(st==200, "list projects")
demo=[p for p in projs if p["slug"]=="demo"][0]
check(demo["my_role"]=="owner","admin is owner of demo (auto-join)")

# 2. create project with owner alice (not yet logged in -> invite)
st,p,_=call("POST","/api/projects",{"slug":"erp-test","name":"ERP test","owner_username":"alice","init":True})
check(st==201, f"create project -> {st} {p}")
pid=p["id"]
st,creds,_=call("GET",f"/api/projects/{pid}/credentials"); check(st==200 and len(creds)==3, f"credentials issued: {[c['kind'] for c in creds]}")
smtp=[c for c in creds if c["kind"]=="smtp"][0]; api=[c for c in creds if c["kind"]=="api"][0]; ses=[c for c in creds if c["kind"]=="ses"][0]
apikey=api["username"]+"."+api["secret"]
st,members,_=call("GET",f"/api/projects/{pid}/members"); check(any(m["username"]=="alice" and m["pending"] for m in members),"alice is pending invite")

# 3. SMTP plain with AUTH
def send_smtp(cls=smtplib.SMTP, port=11025, starttls=False, subject="plain", html=False, attach=False):
    msg=EmailMessage(); msg["From"]="App <noreply@erp.example>"; msg["To"]="Real Customer <customer@gmail.com>"; msg["Cc"]="cc@x.test"; msg["Subject"]=f"SMTP {subject} Xin chào"
    msg.set_content("Hello plain text body with password reset link http://x/y")
    if html: msg.add_alternative("<html><body><h1 style='color:red'>Hi</h1><img src='http://tracker.example/pixel.gif'><img src='cid:logo'><script>alert(1)</script><a href='http://x/reset'>reset</a></body></html>", subtype="html")
    if attach: msg.add_attachment(b"PDFDATA", maintype="application", subtype="pdf", filename="invoice.pdf")
    if attach and html: msg.get_payload()[1].add_related(b"GIF89a", maintype="image", subtype="gif", cid="<logo>")
    kw={}
    if cls is smtplib.SMTP_SSL: kw["context"]=ssl._create_unverified_context()
    s=cls("localhost",port,timeout=10,**kw)
    if starttls: s.starttls(context=ssl._create_unverified_context())
    s.login(smtp["username"],smtp["secret"])
    r=s.send_message(msg); s.quit(); return r

send_smtp(subject="plain"); print("PASS smtp plain auth send")
send_smtp(starttls=True, subject="starttls", html=True, attach=True); print("PASS smtp starttls send (html+attach)")
send_smtp(cls=smtplib.SMTP_SSL, port=11465, subject="smtps"); print("PASS smtps implicit tls send")

# unauthenticated must be rejected
try:
    s=smtplib.SMTP("localhost",11025,timeout=10); s.sendmail("a@b","c@d","Subject: x\n\nhi"); s.quit(); check(False,"unauth smtp rejected")
except smtplib.SMTPException as e:
    check("530" in str(e) or "Authentication required" in str(e), f"unauth smtp rejected: {str(e)[:60]}")
# wrong password
try:
    s=smtplib.SMTP("localhost",11025,timeout=10); s.login(smtp["username"],"bad"); check(False,"bad password rejected")
except smtplib.SMTPAuthenticationError as e:
    check(True, "bad smtp password rejected")
# AUTH LOGIN mechanism
s=smtplib.SMTP("localhost",11025,timeout=10); s.ehlo()
code,resp=s.docmd("AUTH LOGIN", base64.b64encode(smtp["username"].encode()).decode()); code,resp=s.docmd(base64.b64encode(smtp["secret"].encode()).decode())
check(code==235, f"AUTH LOGIN mechanism -> {code} {resp}"); s.quit()

# 4. SendGrid API
body={"personalizations":[{"to":[{"email":"a@x.test","name":"A"}],"subject":"SG personalized"}],"from":{"email":"sg@erp.example","name":"SG"},"subject":"SG","content":[{"type":"text/plain","value":"sendgrid text"},{"type":"text/html","value":"<b>sendgrid html</b>"}],"attachments":[{"content":base64.b64encode(b"csv,data").decode(),"type":"text/csv","filename":"r.csv"}]}
st,_,h=call("POST","/sendgrid/v3/mail/send",body,{"Authorization":"Bearer "+apikey}); check(st==202 and h.get("X-Message-Id"), f"sendgrid send -> {st}")
st,_,_=call("POST","/sendgrid/v3/mail/send",body,{"Authorization":"Bearer bad.key"}); check(st==401,"sendgrid bad key -> 401")

# 5. SES v2 + v1
sesauth={"Authorization":f"AWS4-HMAC-SHA256 Credential={ses['username']}/20260101/ap-southeast-1/ses/aws4_request, SignedHeaders=host, Signature=abc"}
st,r,_=call("POST","/ses/v2/email/outbound-emails",{"FromEmailAddress":"ses@erp.example","Destination":{"ToAddresses":["v2@x.test"]},"Content":{"Simple":{"Subject":{"Data":"SES v2"},"Body":{"Html":{"Data":"<p>ses v2</p>"}}}}},sesauth)
check(st==200 and r.get("MessageId"), f"ses v2 -> {st} {r}")
form="Action=SendEmail&Source=ses1%40erp.example&Destination.ToAddresses.member.1=v1%40x.test&Message.Subject.Data=SES+v1&Message.Body.Text.Data=hello+v1"
st,r,_=call("POST","/ses/",form.encode(),{**sesauth,"Content-Type":"application/x-www-form-urlencoded"},raw=True)
check(st==200 and b"<SendEmailResult><MessageId>" in r, f"ses v1 -> {st} {r[:120]}")
st,r,_=call("POST","/ses/",form.encode(),{"Authorization":"AWS4-HMAC-SHA256 Credential=AKIANOPE/x/","Content-Type":"application/x-www-form-urlencoded"},raw=True); check(st==403,"ses unknown key -> 403")

# 5b. Azure Communication Services (HMAC-SHA256 like the SDK), Microsoft Graph, Gmail
import hmac, hashlib, datetime
acs_body=json.dumps({"senderAddress":"acs@erp.example","content":{"subject":"Azure ACS","plainText":"acs text","html":"<b>acs</b>"},"recipients":{"to":[{"address":"az@x.test","displayName":"Az"}]},"attachments":[{"name":"a.txt","contentType":"text/plain","contentInBase64":base64.b64encode(b"hello").decode()}]}).encode()
path=f"/azure/{api['username']}/emails:send?api-version=2023-03-31"
date=datetime.datetime.now(datetime.timezone.utc).strftime("%a, %d %b %Y %H:%M:%S GMT")
csha=base64.b64encode(hashlib.sha256(acs_body).digest()).decode()
accesskey=base64.b64encode(api["secret"].encode()).decode()   # what the connection string carries
sts=f"POST\n{path}\n{date};localhost:18080;{csha}"
sig=base64.b64encode(hmac.new(base64.b64decode(accesskey), sts.encode(), hashlib.sha256).digest()).decode()
st,r,h=call("POST",path,acs_body,{"Authorization":f"HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature={sig}","x-ms-date":date,"x-ms-content-sha256":csha})
check(st==202 and r["status"]=="Succeeded" and h.get("Operation-Location"), f"azure ACS send with HMAC -> {st} {r}")
st,r,_=call("GET",f"/azure/{api['username']}/emails/operations/{r['id']}?api-version=2023-03-31"); check(st==200 and r["status"]=="Succeeded","azure ACS operation poll")
st,r,_=call("POST",path,acs_body,{"Authorization":"HMAC-SHA256 SignedHeaders=x-ms-date;host;x-ms-content-sha256&Signature=bad","x-ms-date":date,"x-ms-content-sha256":csha}); check(st==401,"azure ACS bad signature -> 401")
graph={"message":{"subject":"Graph mail","body":{"contentType":"HTML","content":"<i>graph</i>"},"toRecipients":[{"emailAddress":{"address":"g@x.test","name":"G"}}],"attachments":[{"@odata.type":"#microsoft.graph.fileAttachment","name":"g.txt","contentType":"text/plain","contentBytes":base64.b64encode(b"gg").decode()}]},"saveToSentItems":False}
st,r,_=call("POST",f"/graph/{api['username']}/v1.0/users/app@erp.example/sendMail",graph,{"Authorization":"Bearer "+apikey}); check(st==202,f"graph sendMail -> {st}")
st,r,_=call("POST",f"/graph/{api['username']}/v1.0/me/sendMail",graph,{"Authorization":"Bearer wrong"}); check(st==401,"graph bad token -> 401")
rawmsg=b"From: gm@erp.example\r\nTo: gm@x.test\r\nSubject: Gmail raw\r\n\r\ngmail body"
st,r,_=call("POST",f"/gmail/{api['username']}/gmail/v1/users/me/messages/send",{"raw":base64.urlsafe_b64encode(rawmsg).decode().rstrip("=")},{"Authorization":"Bearer "+api["secret"]}); check(st==200 and r.get("id") and "SENT" in r["labelIds"],f"gmail messages.send -> {st}")
st,r,_=call("POST",f"/gmail/{api['username']}/upload/gmail/v1/users/me/messages/send?uploadType=media",rawmsg,{"Content-Type":"message/rfc822","Authorization":"Bearer "+apikey}); check(st==200,"gmail media upload send")

# 6. generic API JSON + raw
st,r,_=call("POST","/api/v1/send",{"from":"gen@erp.example","to":["g@x.test"],"subject":"Generic","text":"t","html":"<i>h</i>"},{"X-API-Key":apikey}); check(st==202,f"generic json -> {st} {r}")
st,r,_=call("POST","/api/v1/send?from=raw@erp.example&to=raw@x.test",b"Subject: Raw EML\r\nFrom: raw@erp.example\r\nTo: raw@x.test\r\n\r\nraw body",{"Content-Type":"message/rfc822","X-API-Key":apikey},); check(st==202,f"generic raw -> {st}")

# 7. REST read
st,lst,_=call("GET",f"/api/projects/{pid}/emails"); check(st==200 and lst["total"]==12, f"email list total={lst['total']}")
st,lst,_=call("GET",f"/api/projects/{pid}/emails?q=starttls"); check(lst["total"]==1 and lst["items"][0]["attachment_count"]==2, "search by subject + attachment count")
st,lst,_=call("GET",f"/api/projects/{pid}/emails?q=customer"); check(lst["total"]==3, f"search by recipient local part -> {lst['total']}")
eid=[e for e in call("GET",f"/api/projects/{pid}/emails")[1]["items"] if "starttls" in e["subject"]][0]["id"]
st,e,_=call("GET",f"/api/projects/{pid}/emails/{eid}"); check(st==200 and e["seen"] and e["html_body"] and len(e["attachments"])==2 and e["text_body"].startswith("Hello"), "get email full")
st,h,_=call("GET",f"/api/projects/{pid}/emails/{eid}/html",raw=True); h=h.decode()
check("<script" not in h and "cid/logo" in h and "tracker.example" in h, "html sanitized, cid rewritten, remote kept (blocked by CSP)")
st,raw,_=call("GET",f"/api/projects/{pid}/emails/{eid}/raw",raw=True); check(b"Subject: SMTP starttls" in raw, "raw download")
st,st2,_=call("GET",f"/api/projects/{pid}/stats"); check(st2["total"]==12 and st2["unread"]==11, f"stats {st2}")
st,lst,_=call("GET",f"/api/projects/{pid}/emails?q=Azure"); check(lst["items"][0]["source"]=="azure" and lst["items"][0]["attachment_count"]==1,"azure mail stored with source and attachment")

# 8. RBAC: alice logs in -> invite materialized -> owner; bob -> member of demo only
cj.clear(); st,alice,_=call("POST","/api/auth/login",{"username":"alice","password":"dev"}); check(st==200 and alice["role"]=="user","alice login")
st,projs,_=call("GET","/api/projects"); roles={p["slug"]:p["my_role"] for p in projs}; check(roles.get("erp-test")=="owner" and roles.get("demo")=="member", f"alice roles {roles}")
st,r,_=call("POST",f"/api/projects/{pid}/members",{"username":"bob","role":"member"}); check(st==200,"alice adds bob")
st,r,_=call("GET","/api/admin/users"); check(st==403,"alice cannot access admin")
cj.clear(); st,bob,_=call("POST","/api/auth/login",{"username":"bob","password":"dev"}); check(st==200,"bob login")
st,projs,_=call("GET","/api/projects"); roles={p["slug"]:p["my_role"] for p in projs}; check(roles.get("erp-test")=="member","bob is member")
st,r,_=call("POST",f"/api/projects/{pid}/init?reinit=1"); check(st==403,"bob cannot reinit")
st,r,_=call("GET",f"/api/projects/{pid}/credentials"); check(st==200 and len(r)==3,"bob can view credentials")
st,r,_=call("DELETE",f"/api/projects/{pid}/emails/{eid}"); check(st==204,"bob can delete an email")
cj.clear(); st,carol,_=call("POST","/api/auth/login",{"username":"carol","password":"dev"})
st,r,_=call("GET",f"/api/projects/{pid}/emails"); check(st==404,"carol (not member) gets 404")
# auditor
cj.clear(); call("POST","/api/auth/login",{"username":"admin","password":"dev"})
st,users,_=call("GET","/api/admin/users"); cid=[u for u in users if u["username"]=="carol"][0]["id"]
st,r,_=call("PATCH",f"/api/admin/users/{cid}",{"role":"auditor"}); check(st==200 and r["role"]=="auditor","admin promotes carol to auditor")
cj.clear(); call("POST","/api/auth/login",{"username":"carol","password":"dev"})
st,r,_=call("GET",f"/api/projects/{pid}/emails"); check(st==200,"auditor reads any project")
st,r,_=call("DELETE",f"/api/projects/{pid}/emails"); check(st==403,"auditor cannot clear inbox")
st,r,_=call("GET","/api/admin/audit?limit=5"); check(st==200 and r["total"]>10, f"auditor reads audit log ({r['total']} entries)")
st,r,_=call("GET","/api/admin/audit?protocol=smtp&action=smtp_login"); check(r["total"]>=4 and r["items"][0]["ip"] and r["items"][0]["protocol"]=="smtp", f"audit records smtp logins with ip ({r['total']})")
st,r,_=call("GET","/api/admin/audit?action=smtp_login_failed"); check(r["total"]>=1, "audit records failed smtp login")
st,r,_=call("GET","/api/admin/audit?action=mail_captured&protocol=ses"); check(r["total"]==2, f"audit records captured mail per protocol (ses={r['total']})")
st,r,_=call("GET","/api/admin/audit?action=api_auth_failed"); check(r["total"]>=2, "audit records bad api keys")
cj.clear(); call("POST","/api/auth/login",{"username":"admin","password":"dev"})
st,r,_=call("GET","/api/admin/audit"); check(st==200,"admin reads the global audit log (Logs tab)")
st,r,_=call("GET",f"/api/projects/{pid}/audit?protocol=imap"); check(st==200,"admin reads a project's audit log")
cj.clear(); call("POST","/api/auth/login",{"username":"alice","password":"dev"})
st,r,_=call("GET",f"/api/projects/{pid}/audit"); check(st==200 and r["total"]>0 and all(x["project_slug"]=="erp-test" for x in r["items"]), "owner reads own project audit only")
cj.clear(); call("POST","/api/auth/login",{"username":"carol","password":"dev"})
# reinit rotates
cj.clear(); call("POST","/api/auth/login",{"username":"alice","password":"dev"})
st,newcreds,_=call("POST",f"/api/projects/{pid}/init?reinit=1"); check(st==200,"owner reinit")
newsmtp=[c for c in newcreds if c["kind"]=="smtp"][0]
try:
    s=smtplib.SMTP("localhost",11025,timeout=10); s.login(smtp["username"],smtp["secret"]); check(False,"old smtp cred revoked")
except smtplib.SMTPAuthenticationError: check(True,"old smtp cred revoked after reinit")
smtp=newsmtp

# 9. IMAP
M=imaplib.IMAP4("localhost",11143); M.starttls(ssl._create_unverified_context()); M.login(smtp["username"],smtp["secret"])
typ,data=M.list(); check(typ=="OK" and b"INBOX" in data[0], f"imap list {data}")
typ,data=M.select("INBOX"); check(typ=="OK" and int(data[0])==11, f"imap select exists={data}")
typ,data=M.search(None,"UNSEEN"); check(typ=="OK" and len(data[0].split())==11, f"imap search unseen -> {data}")
typ,data=M.fetch("1","(UID FLAGS RFC822.SIZE ENVELOPE BODY.PEEK[HEADER.FIELDS (SUBJECT)])"); check(typ=="OK" and b"Subject: SMTP plain" in b"".join(x for t in data if isinstance(t,tuple) for x in t), "imap fetch envelope/header")
typ,data=M.fetch("1","(BODY[])"); check(b"Hello plain text" in data[0][1],"imap fetch body marks seen")
typ,data=M.fetch("1","(FLAGS)"); check(b"\\Seen" in data[0],"imap seen flag set")
typ,data=M.store("2","+FLAGS","(\\Deleted)"); typ,data=M.expunge(); check(typ=="OK","imap expunge")
typ,data=M.select("INBOX"); check(int(data[0])==10,"imap after expunge 10 msgs")
typ,data=M.uid("search",None,"SUBJECT","SES"); check(len(data[0].split())==2, f"imap uid search subject -> {data}")
M.logout()
Ms=imaplib.IMAP4_SSL("localhost",11993,ssl_context=ssl._create_unverified_context()); Ms.login(smtp["username"],smtp["secret"]); typ,d=Ms.status("INBOX","(MESSAGES UNSEEN)"); check(typ=="OK" and b"MESSAGES 10" in d[0],f"imaps status {d}"); Ms.logout()
try:
    X=imaplib.IMAP4("localhost",11143); X.login("demo","wrong"); check(False,"imap bad login")
except imaplib.IMAP4.error: check(True,"imap bad login rejected")

# POP3
import poplib
P=poplib.POP3("localhost",11110,timeout=10); P.stls(context=ssl._create_unverified_context()); P.user(smtp["username"]); P.pass_(smtp["secret"])
n,size=P.stat(); check(n==10, f"pop3 STLS stat -> {n} msgs")
resp,lst,_=P.uidl(); check(len(lst)==10 and len(lst[0].split()[1])==32, "pop3 UIDL")
resp,lines,octets=P.retr(1); body=b"\r\n".join(lines); check(b"Subject:" in body and octets>0, "pop3 RETR")
resp,lines,_=P.top(1,0); check(b"Subject:" in b"\n".join(lines), "pop3 TOP")
P.dele(1); P.quit()
P=poplib.POP3_SSL("localhost",11995,timeout=10,context=ssl._create_unverified_context()); P.user(smtp["username"]); P.pass_(smtp["secret"]); n,_=P.stat(); check(n==9, f"pop3s after DELE+QUIT -> {n} msgs"); P.quit()
try:
    P=poplib.POP3("localhost",11110,timeout=10); P.user(smtp["username"]); P.pass_("bad"); check(False,"pop3 bad login")
except poplib.error_proto: check(True,"pop3 bad login rejected")

# 10. SSE receives new mail
import threading, socket
got=[]
def sse():
    req=urllib.request.Request(API+f"/api/projects/{pid}/events"); r=op.open(req,timeout=15)
    for line in r:
        if line.startswith(b"event: new"): got.append(1); break
t=threading.Thread(target=sse,daemon=True); t.start(); time.sleep(0.5)
send_smtp(subject="sse"); t.join(5); check(got,"SSE delivered new-mail event")
# retention/max_emails
st,r,_=call("PATCH",f"/api/projects/{pid}",{"max_emails":10}); check(st==403,"owner cannot change retention limits (admin only)")
st,r,_=call("PATCH",f"/api/projects/{pid}",{"name":"ERP test renamed"}); check(st==200 and r["name"]=="ERP test renamed","owner updates name")
cj.clear(); call("POST","/api/auth/login",{"username":"admin","password":"dev"})
st,r,_=call("PATCH",f"/api/projects/{pid}",{"retention_days":181}); check(st==400,"retention above 180 rejected")
st,r,_=call("PATCH",f"/api/projects/{pid}",{"retention_days":180,"max_emails":10}); check(st==200 and r["retention_days"]==180,"admin sets retention 180")
st,p2,_=call("POST","/api/projects",{"slug":"ret-default","name":"r"}); check(st==201 and p2["retention_days"]==14, f"new project defaults to 14 days ({p2.get('retention_days')})")
print("ALL PASS")
