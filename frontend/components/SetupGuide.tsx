"use client";

import { useMemo, useState } from "react";
import { Check, Copy, Mail, Inbox, Code2, Send, Cloud, CloudCog, Globe } from "lucide-react";
import type { Credential, ServerConfig } from "@/lib/api";
import { useT } from "@/lib/i18n";

type Lang = "settings" | "node" | "java" | "dotnet" | "python" | "php" | "go" | "curl";
const LANG_LABEL: Record<Lang, string> = { settings: "Settings", node: "Node.js", java: "Java", dotnet: ".NET", python: "Python", php: "PHP", go: "Go", curl: "curl" };

interface Snippet { lang: Lang; title: string; code: string }
interface Provider { key: string; icon: React.ReactNode; intro: string; snippets: Snippet[] }

function CodeBlock({ title, code }: { title: string; code: string }) {
  const [ok, setOk] = useState(false);
  return (
    <div className="mb-3">
      <div className="mb-1 flex items-center justify-between">
        <span className="text-sm font-medium">{title}</span>
        <button
          className="btn btn-sm"
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(code);
              setOk(true);
              setTimeout(() => setOk(false), 1200);
            } catch {
              /* ignore */
            }
          }}
        >
          {ok ? <Check size={13} style={{ color: "var(--ok)" }} /> : <Copy size={13} />}
        </button>
      </div>
      <pre className="code">{code}</pre>
    </div>
  );
}

export default function SetupGuide({ cfg, creds }: { cfg: ServerConfig; creds: Credential[] }) {
  const { t } = useT();
  const smtp = creds.find((c) => c.kind === "smtp");
  const apiKey = creds.find((c) => c.kind === "api");
  const ses = creds.find((c) => c.kind === "ses");
  const host = cfg.mail_host;
  const smtpPort = cfg.smtp_ports[0] || "25";
  const subPort = cfg.smtp_ports[1] || cfg.smtp_ports[0] || "587";
  const smtpsPort = cfg.smtps_ports[0];
  const imapPort = cfg.imap_ports[0];
  const imapsPort = cfg.imaps_ports[0];
  const pop3Port = cfg.pop3_ports?.[0];
  const pop3sPort = cfg.pop3s_ports?.[0];
  const user = smtp?.username || "<smtp-username>";
  const pass = smtp?.secret || "<smtp-password>";
  const key = apiKey ? `${apiKey.username}.${apiKey.secret}` : "<api-key>";
  const keyId = apiKey?.username || "<key-id>";
  const accessKeyB64 = apiKey?.secret ? btoa(apiKey.secret) : "<base64(secret)>";
  const base = cfg.public_url;
  const baseHost = base.replace(/^https?:\/\//, "");

  const providers: Provider[] = useMemo(() => [
    {
      key: "smtp", icon: <Mail size={15} />, intro: t("guide.smtp.intro"),
      snippets: [
        { lang: "settings", title: t("guide.settings"), code: `Host:      ${host}\nPort:      ${subPort} (STARTTLS)  ·  ${smtpPort} (plain/STARTTLS)${smtpsPort ? `  ·  ${smtpsPort} (TLS)` : ""}\nUsername:  ${user}\nPassword:  ${pass}\nAuth:      PLAIN or LOGIN\n${t("guide.anyAddress")}` },
        { lang: "node", title: "nodemailer", code: `const transport = nodemailer.createTransport({\n  host: "${host}", port: ${subPort}, secure: false, // STARTTLS\n  auth: { user: "${user}", pass: "${pass}" },\n});` },
        { lang: "java", title: "Spring Boot application.yml", code: `spring:\n  mail:\n    host: ${host}\n    port: ${subPort}\n    username: ${user}\n    password: ${pass}\n    properties:\n      mail.smtp.auth: true\n      mail.smtp.starttls.enable: true` },
        { lang: "java", title: "jakarta.mail", code: `Properties p = new Properties();\np.put("mail.smtp.host", "${host}"); p.put("mail.smtp.port", "${subPort}");\np.put("mail.smtp.auth", "true"); p.put("mail.smtp.starttls.enable", "true");\nSession s = Session.getInstance(p, new Authenticator() {\n  protected PasswordAuthentication getPasswordAuthentication() {\n    return new PasswordAuthentication("${user}", "${pass}");\n  }\n});` },
        { lang: "dotnet", title: "System.Net.Mail", code: `var client = new SmtpClient("${host}", ${subPort}) {\n  EnableSsl = true, // STARTTLS\n  Credentials = new NetworkCredential("${user}", "${pass}")\n};` },
        { lang: "dotnet", title: "MailKit", code: `using var client = new SmtpClient();\nawait client.ConnectAsync("${host}", ${subPort}, SecureSocketOptions.StartTls);\nawait client.AuthenticateAsync("${user}", "${pass}");` },
        { lang: "python", title: "Django settings.py", code: `EMAIL_HOST = "${host}"\nEMAIL_PORT = ${subPort}\nEMAIL_HOST_USER = "${user}"\nEMAIL_HOST_PASSWORD = "${pass}"\nEMAIL_USE_TLS = True` },
        { lang: "python", title: "smtplib", code: `s = smtplib.SMTP("${host}", ${subPort}); s.starttls()\ns.login("${user}", "${pass}")\ns.send_message(msg)` },
        { lang: "php", title: "Laravel .env", code: `MAIL_MAILER=smtp\nMAIL_HOST=${host}\nMAIL_PORT=${subPort}\nMAIL_USERNAME=${user}\nMAIL_PASSWORD=${pass}\nMAIL_ENCRYPTION=tls` },
        { lang: "php", title: "PHPMailer", code: `$mail->isSMTP(); $mail->Host = '${host}'; $mail->Port = ${subPort};\n$mail->SMTPAuth = true; $mail->SMTPSecure = PHPMailer::ENCRYPTION_STARTTLS;\n$mail->Username = '${user}'; $mail->Password = '${pass}';` },
        { lang: "go", title: "net/smtp", code: `auth := smtp.PlainAuth("", "${user}", "${pass}", "${host}")\nerr := smtp.SendMail("${host}:${subPort}", auth, "app@example.com", []string{"user@example.com"}, msg) // STARTTLS automatic` },
        { lang: "curl", title: "swaks", code: `swaks --to anyone@example.com --from app@example.com --server ${host}:${subPort} --auth-user ${user} --auth-password '${pass}' --tls` },
      ],
    },
    {
      key: "outlook", icon: <Inbox size={15} />, intro: t("guide.outlook.intro"),
      snippets: [
        { lang: "settings", title: t("guide.outlook.incoming"), code: `IMAP:  ${host}  ${imapsPort ? `${imapsPort} (SSL/TLS)` : ""}${imapPort ? `  ·  ${imapPort} (STARTTLS)` : ""}\n${pop3sPort || pop3Port ? `POP3:  ${host}  ${pop3sPort ? `${pop3sPort} (SSL/TLS)` : ""}${pop3Port ? `  ·  ${pop3Port} (STLS)` : ""}\n` : ""}Username:  ${user}\nPassword:  ${pass}` },
        { lang: "settings", title: t("guide.outlook.outgoing"), code: `SMTP:  ${host}  ${smtpsPort ? `${smtpsPort} (SSL/TLS)` : ""}  ·  ${subPort} (STARTTLS)\nUsername:  ${user}\nPassword:  ${pass}\n\n${t("guide.outlook.steps", { user })}` },
      ],
    },
    {
      key: "api", icon: <Code2 size={15} />, intro: t("guide.api.intro"),
      snippets: [
        { lang: "settings", title: "Endpoint", code: `POST ${base}/api/v1/send\nX-API-Key: ${key}        (or Authorization: Bearer ${key})\nContent-Type: application/json | message/rfc822` },
        { lang: "curl", title: t("guide.api.json"), code: `curl -X POST ${base}/api/v1/send -H "X-API-Key: ${key}" -H "Content-Type: application/json" \\\n  -d '{"from":"app@example.com","to":["user@example.com"],"subject":"Hello","text":"Hi","html":"<b>Hi</b>"}'` },
        { lang: "curl", title: t("guide.api.raw"), code: `curl -X POST "${base}/api/v1/send?from=app@example.com&to=user@example.com" -H "X-API-Key: ${key}" -H "Content-Type: message/rfc822" --data-binary @message.eml` },
        { lang: "node", title: "fetch", code: `await fetch("${base}/api/v1/send", { method: "POST", headers: { "X-API-Key": "${key}", "Content-Type": "application/json" },\n  body: JSON.stringify({ from: "app@example.com", to: ["user@example.com"], subject: "Hello", html: "<b>Hi</b>" }) });` },
        { lang: "python", title: "requests", code: `requests.post("${base}/api/v1/send", headers={"X-API-Key": "${key}"},\n              json={"from": "app@example.com", "to": ["user@example.com"], "subject": "Hello", "text": "Hi"})` },
        { lang: "settings", title: t("guide.api.read"), code: `GET ${base}/api/projects/<project-id>/emails?q=verification\nGET ${base}/api/projects/<project-id>/emails/<id>\nGET ${base}/api/projects/<project-id>/events      # Server-Sent Events` },
      ],
    },
    {
      key: "sendgrid", icon: <Send size={15} />, intro: t("guide.sendgrid.intro"),
      snippets: [
        { lang: "settings", title: "Endpoint", code: `Host:     ${base}/sendgrid\nAPI key:  ${key}` },
        { lang: "node", title: "@sendgrid/mail", code: `const sgMail = require("@sendgrid/mail");\nsgMail.setApiKey("${key}");\nsgMail.setClient(new (require("@sendgrid/client"))());\nsgMail.client.setDefaultRequest("baseUrl", "${base}/sendgrid");` },
        { lang: "python", title: "sendgrid", code: `sg = sendgrid.SendGridAPIClient(api_key="${key}", host="${base}/sendgrid")` },
        { lang: "java", title: "sendgrid-java", code: `SendGrid sg = new SendGrid("${key}");\nsg.setHost("${baseHost}/sendgrid");` },
        { lang: "dotnet", title: "SendGrid C#", code: `var client = new SendGridClient(new SendGridClientOptions { ApiKey = "${key}", Host = "${base}/sendgrid" });` },
        { lang: "php", title: "sendgrid-php", code: `$sg = new \\SendGrid('${key}', ['host' => '${base}/sendgrid']);` },
        { lang: "go", title: "sendgrid-go", code: `req := sendgrid.GetRequest("${key}", "/v3/mail/send", "${base}/sendgrid")\nreq.Method = "POST"; req.Body = mail.GetRequestBody(m); sendgrid.API(req)` },
        { lang: "curl", title: "curl", code: `curl -X POST ${base}/sendgrid/v3/mail/send -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \\\n  -d '{"personalizations":[{"to":[{"email":"u@example.com"}]}],"from":{"email":"app@example.com"},"subject":"Hi","content":[{"type":"text/plain","value":"Hello"}]}'` },
      ],
    },
    {
      key: "ses", icon: <Cloud size={15} />, intro: t("guide.ses.intro"),
      snippets: [
        { lang: "settings", title: t("guide.ses.credentials"), code: `AWS_ACCESS_KEY_ID=${ses?.username || "<access-key-id>"}\nAWS_SECRET_ACCESS_KEY=${ses?.secret || "<secret>"}\nAWS_REGION=ap-southeast-1\nEndpoint: ${base}/ses` },
        { lang: "node", title: "AWS SDK v3 (SESv2)", code: `const client = new SESv2Client({ region: "ap-southeast-1", endpoint: "${base}/ses",\n  credentials: { accessKeyId: "${ses?.username || "AKIA..."}", secretAccessKey: "${ses?.secret || "..."}" } });` },
        { lang: "python", title: "boto3", code: `ses = boto3.client("sesv2", region_name="ap-southeast-1", endpoint_url="${base}/ses",\n    aws_access_key_id="${ses?.username || "AKIA..."}", aws_secret_access_key="${ses?.secret || "..."}")` },
        { lang: "java", title: "AWS SDK v2", code: `SesV2Client.builder().region(Region.AP_SOUTHEAST_1).endpointOverride(URI.create("${base}/ses"))\n  .credentialsProvider(StaticCredentialsProvider.create(AwsBasicCredentials.create("${ses?.username || "AKIA..."}", "${ses?.secret || "..."}"))).build();` },
        { lang: "dotnet", title: "AWSSDK.SimpleEmailV2", code: `var cfg = new AmazonSimpleEmailServiceV2Config { ServiceURL = "${base}/ses", AuthenticationRegion = "ap-southeast-1" };\nvar client = new AmazonSimpleEmailServiceV2Client("${ses?.username || "AKIA..."}", "${ses?.secret || "..."}", cfg);` },
        { lang: "php", title: "aws-sdk-php", code: `$ses = new Aws\\SesV2\\SesV2Client(['region' => 'ap-southeast-1', 'version' => 'latest', 'endpoint' => '${base}/ses',\n  'credentials' => ['key' => '${ses?.username || "AKIA..."}', 'secret' => '${ses?.secret || "..."}']]);` },
        { lang: "go", title: "aws-sdk-go-v2", code: `client := sesv2.NewFromConfig(cfg, func(o *sesv2.Options) { o.BaseEndpoint = aws.String("${base}/ses") })` },
      ],
    },
    {
      key: "azure", icon: <CloudCog size={15} />, intro: t("guide.azure.intro"),
      snippets: [
        { lang: "settings", title: "Connection string / Graph base URL", code: `ACS:    endpoint=${base}/azure/${keyId};accesskey=${accessKeyB64}\nGraph:  ${base}/graph/${keyId}/v1.0    Authorization: Bearer ${key}` },
        { lang: "dotnet", title: "Azure.Communication.Email", code: `var client = new EmailClient("endpoint=${base}/azure/${keyId};accesskey=${accessKeyB64}");\nawait client.SendAsync(WaitUntil.Completed, "app@example.com", "user@example.com", "Hello", "<b>Hi</b>");` },
        { lang: "dotnet", title: "Microsoft Graph", code: `var graph = new GraphServiceClient(httpClient) { BaseUrl = "${base}/graph/${keyId}/v1.0" }; // bearer = API key` },
        { lang: "java", title: "azure-communication-email", code: `EmailClient client = new EmailClientBuilder()\n    .connectionString("endpoint=${base}/azure/${keyId};accesskey=${accessKeyB64}").buildClient();` },
        { lang: "java", title: "Microsoft Graph", code: `graphClient.setServiceRoot("${base}/graph/${keyId}/v1.0"); // bearer = API key` },
        { lang: "python", title: "azure-communication-email", code: `client = EmailClient.from_connection_string("endpoint=${base}/azure/${keyId};accesskey=${accessKeyB64}")\npoller = client.begin_send({"senderAddress": "app@example.com", "recipients": {"to": [{"address": "user@example.com"}]},\n                            "content": {"subject": "Hello", "plainText": "Hi"}})` },
        { lang: "node", title: "@azure/communication-email", code: `const client = new EmailClient("endpoint=${base}/azure/${keyId};accesskey=${accessKeyB64}");\nconst poller = await client.beginSend({ senderAddress: "app@example.com", recipients: { to: [{ address: "user@example.com" }] }, content: { subject: "Hello", plainText: "Hi" } });` },
        { lang: "node", title: "Microsoft Graph", code: `const client = Client.initWithMiddleware({ baseUrl: "${base}/graph/${keyId}/v1.0", authProvider: { getAccessToken: async () => "${key}" } });\nawait client.api("/users/app@example.com/sendMail").post({ message: { subject: "Hello", body: { contentType: "HTML", content: "<b>Hi</b>" }, toRecipients: [{ emailAddress: { address: "user@example.com" } }] } });` },
        { lang: "curl", title: "Graph sendMail", code: `curl -X POST ${base}/graph/${keyId}/v1.0/users/app@example.com/sendMail -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \\\n  -d '{"message":{"subject":"Hello","body":{"contentType":"HTML","content":"<b>Hi</b>"},"toRecipients":[{"emailAddress":{"address":"user@example.com"}}]}}'` },
      ],
    },
    {
      key: "gmail", icon: <Globe size={15} />, intro: t("guide.gmail.intro"),
      snippets: [
        { lang: "settings", title: "Endpoint", code: `Root URL:  ${base}/gmail/${keyId}\nBearer:    ${key}\nPOST ${base}/gmail/${keyId}/gmail/v1/users/me/messages/send   {"raw": "<base64url RFC 822>"}` },
        { lang: "python", title: "google-api-python-client", code: `creds = google.oauth2.credentials.Credentials(token="${key}")\nservice = build("gmail", "v1", credentials=creds, client_options={"api_endpoint": "${base}/gmail/${keyId}"})\nservice.users().messages().send(userId="me", body={"raw": base64.urlsafe_b64encode(msg.as_bytes()).decode()}).execute()` },
        { lang: "node", title: "googleapis", code: `const gmail = google.gmail({ version: "v1", auth: oauth2, rootUrl: "${base}/gmail/${keyId}" }); // token = "${key}"\nawait gmail.users.messages.send({ userId: "me", requestBody: { raw } });` },
        { lang: "java", title: "google-api-services-gmail", code: `Gmail gmail = new Gmail.Builder(transport, jsonFactory, req -> req.getHeaders().setAuthorization("Bearer ${key}"))\n    .setRootUrl("${base}/gmail/${keyId}/").build();` },
        { lang: "go", title: "google.golang.org/api/gmail/v1", code: `svc, _ := gmail.NewService(ctx, option.WithEndpoint("${base}/gmail/${keyId}/"),\n  option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "${key}"})))\nsvc.Users.Messages.Send("me", &gmail.Message{Raw: raw}).Do()` },
        { lang: "curl", title: "curl", code: `curl -X POST ${base}/gmail/${keyId}/gmail/v1/users/me/messages/send -H "Authorization: Bearer ${key}" -H "Content-Type: application/json" \\\n  -d '{"raw":"'$(printf 'From: a@b\\r\\nTo: c@d\\r\\nSubject: Hi\\r\\n\\r\\nhello' | base64 -w0 | tr '+/' '-_' | tr -d =)'"}'` },
      ],
    },
  ], [t, host, smtpPort, subPort, smtpsPort, imapPort, imapsPort, pop3Port, pop3sPort, user, pass, key, keyId, accessKeyB64, base, baseHost, ses]);

  const [pkey, setPkey] = useState("smtp");
  const [lang, setLang] = useState<Lang>("settings");
  const provider = providers.find((p) => p.key === pkey) || providers[0];
  const langs = Array.from(new Set(provider.snippets.map((s) => s.lang)));
  const activeLang = langs.includes(lang) ? lang : langs[0];
  const shown = provider.snippets.filter((s) => s.lang === activeLang);

  return (
    <div className="panel grid md:grid-cols-[200px_1fr]">
      <nav className="border-b p-2 md:border-b-0 md:border-r" style={{ borderColor: "var(--border)" }}>
        {providers.map((p) => (
          <button key={p.key} className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm ${pkey === p.key ? "row-active font-semibold" : ""}`} onClick={() => setPkey(p.key)}>
            {p.icon} {t(`guide.tab.${p.key}`)}
          </button>
        ))}
      </nav>
      <div className="min-w-0 p-4">
        <p className="muted mb-3 text-sm">{provider.intro}</p>
        <div className="seg mb-3 flex-wrap">
          {langs.map((l) => (
            <button key={l} type="button" className={activeLang === l ? "on" : ""} onClick={() => setLang(l)}>{LANG_LABEL[l]}</button>
          ))}
        </div>
        {shown.map((s, i) => <CodeBlock key={i} title={s.title} code={s.code} />)}
      </div>
    </div>
  );
}
