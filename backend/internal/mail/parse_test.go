package mail

import (
	"net/mail"
	"strings"
	"testing"
)

const sample = "From: App <app@example.com>\r\nTo: Real Customer <c@gmail.com>, b@x.test\r\nCc: cc@x.test\r\nSubject: =?UTF-8?B?WGluIGNow6Bv?= world\r\nDate: Wed, 29 Oct 2026 09:00:00 +0700\r\nMessage-ID: <abc@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"b1\"\r\n\r\n--b1\r\nContent-Type: multipart/alternative; boundary=\"b2\"\r\n\r\n--b2\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain body\r\n--b2\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>html <img src=\"cid:logo\"></p><script>x()</script>\r\n--b2--\r\n--b1\r\nContent-Type: image/gif\r\nContent-ID: <logo>\r\nContent-Disposition: inline\r\n\r\nGIF89a\r\n--b1\r\nContent-Type: application/pdf; name=\"inv.pdf\"\r\nContent-Disposition: attachment; filename=\"inv.pdf\"\r\n\r\n%PDF\r\n--b1--\r\n"

func TestParse(t *testing.T) {
	p := Parse([]byte(sample))
	e := p.Email
	if e.Subject != "Xin chào world" {
		t.Fatalf("subject = %q", e.Subject)
	}
	if e.MessageID != "abc@example.com" {
		t.Fatalf("message id = %q", e.MessageID)
	}
	if len(e.From) != 1 || e.From[0].Address != "app@example.com" || e.From[0].Name != "App" {
		t.Fatalf("from = %+v", e.From)
	}
	if len(e.To) != 2 || len(e.Cc) != 1 {
		t.Fatalf("to=%d cc=%d", len(e.To), len(e.Cc))
	}
	if e.TextBody != "plain body" {
		t.Fatalf("text = %q", e.TextBody)
	}
	if !strings.Contains(e.HTMLBody, "<p>html") {
		t.Fatalf("html = %q", e.HTMLBody)
	}
	if e.SentAt == nil || e.SentAt.Hour() != 9 {
		t.Fatalf("sent_at = %v", e.SentAt)
	}
	if len(p.Attachments) != 2 {
		t.Fatalf("attachments = %d", len(p.Attachments))
	}
	var sawInline, sawPDF bool
	for _, a := range p.Attachments {
		if a.ContentID == "logo" && a.Inline && a.ContentType == "image/gif" {
			sawInline = true
		}
		if a.Filename == "inv.pdf" && !a.Inline && string(a.Data) == "%PDF" {
			sawPDF = true
		}
	}
	if !sawInline || !sawPDF {
		t.Fatalf("attachments = %+v", p.Attachments)
	}
}

func TestParseGarbage(t *testing.T) {
	p := Parse([]byte("not an email at all"))
	if p.Email.TextBody == "" && p.Email.Subject == "" {
		t.Fatal("expected a usable record for unparseable input")
	}
}

func TestSanitizeHTML(t *testing.T) {
	in := `<div onclick="x()" style="color:red"><script>alert(1)</script><img src="cid:logo"><a href="javascript:bad()">l</a><a href="https://ok">ok</a></div>`
	out := SanitizeHTML(in, "/cid/")
	for _, bad := range []string{"<script", "onclick", "javascript:"} {
		if strings.Contains(out, bad) {
			t.Fatalf("sanitized output still contains %q: %s", bad, out)
		}
	}
	if !strings.Contains(out, `src="/cid/logo"`) {
		t.Fatalf("cid not rewritten: %s", out)
	}
	if !strings.Contains(out, `style="color:red"`) {
		t.Fatalf("inline style dropped: %s", out)
	}
}

func TestBuildRoundTrip(t *testing.T) {
	m := &Message{
		From:    mail.Address{Name: "App", Address: "app@example.com"},
		To:      []mail.Address{{Address: "a@x.test"}},
		Bcc:     []mail.Address{{Address: "hidden@x.test"}},
		Subject: "Hello ✓",
		Text:    "text part",
		HTML:    "<b>html part</b>",
		Attachments: []FileAttachment{
			{Filename: "r.csv", ContentType: "text/csv", Data: []byte("a,b")},
			{Filename: "logo.png", ContentType: "image/png", ContentID: "logo", Inline: true, Data: []byte{1, 2, 3}},
		},
	}
	raw, envTo, err := Build(m, "maildev.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(envTo) != 2 || envTo[1] != "hidden@x.test" {
		t.Fatalf("envelope = %v", envTo)
	}
	if strings.Contains(string(raw), "hidden@x.test") {
		t.Fatal("bcc leaked into headers")
	}
	p := Parse(raw)
	if p.Email.Subject != "Hello ✓" || p.Email.TextBody != "text part" || !strings.Contains(p.Email.HTMLBody, "html part") {
		t.Fatalf("round trip mismatch: %+v", p.Email)
	}
	if len(p.Attachments) != 2 {
		t.Fatalf("attachments = %d", len(p.Attachments))
	}
	if p.Email.MessageID == "" || !strings.HasSuffix(p.Email.MessageID, "@maildev.test") {
		t.Fatalf("message id = %q", p.Email.MessageID)
	}
}

func TestDetectSMIME(t *testing.T) {
	signed := "From: a@b\r\nTo: c@d\r\nSubject: s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"x\"\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nhello\r\n--x\r\nContent-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"smime.p7s\"\r\n\r\nMIIB\r\n--x--\r\n"
	p := Parse([]byte(signed))
	if p.Email.SMIME != "signed" || p.Email.TextBody != "hello" || len(p.Attachments) != 1 || p.Attachments[0].Filename != "smime.p7s" {
		t.Fatalf("signed: smime=%q text=%q atts=%+v", p.Email.SMIME, p.Email.TextBody, p.Attachments)
	}
	enc := "From: a@b\r\nTo: c@d\r\nSubject: e\r\nMIME-Version: 1.0\r\nContent-Type: application/pkcs7-mime; smime-type=enveloped-data; name=\"smime.p7m\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"smime.p7m\"\r\n\r\nMIIB\r\n"
	p = Parse([]byte(enc))
	if p.Email.SMIME != "encrypted" || p.Email.TextBody != "" || len(p.Attachments) != 1 {
		t.Fatalf("encrypted: smime=%q text=%q atts=%d", p.Email.SMIME, p.Email.TextBody, len(p.Attachments))
	}
}

func TestNormalizeCRLF(t *testing.T) {
	if got := string(NormalizeCRLF([]byte("a\nb\r\nc\n"))); got != "a\r\nb\r\nc\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := string(NormalizeCRLF([]byte("x\r\ny"))); got != "x\r\ny" {
		t.Fatalf("got %q", got)
	}
}
