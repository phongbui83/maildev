package smime

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
)

type testPKI struct {
	caCert   *x509.Certificate
	leafCert *x509.Certificate
	leafKey  *rsa.PrivateKey
	pool     *x509.CertPool
}

func newPKI(t *testing.T, email string) *testPKI {
	t.Helper()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test Signer"}, EmailAddresses: []string{email},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	leafCert, _ := x509.ParseCertificate(leafDER)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &testPKI{caCert: caCert, leafCert: leafCert, leafKey: leafKey, pool: pool}
}

func (p *testPKI) sign(t *testing.T, content []byte, detached bool) []byte {
	t.Helper()
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := sd.AddSignerChain(p.leafCert, p.leafKey, []*x509.Certificate{p.caCert}, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	if detached {
		sd.Detach()
	}
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func b64(b []byte) string {
	s := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(s) > 76 {
		out.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	out.WriteString(s + "\r\n")
	return out.String()
}

func detachedMessage(content string, sig []byte) []byte {
	return []byte("From: a@b\r\nTo: c@d\r\nSubject: s\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/signed; protocol=\"application/pkcs7-signature\"; micalg=sha-256; boundary=\"bnd\"\r\n\r\n" +
		"--bnd\r\n" + content + "\r\n--bnd\r\n" +
		"Content-Type: application/pkcs7-signature; name=\"smime.p7s\"\r\nContent-Transfer-Encoding: base64\r\nContent-Disposition: attachment; filename=\"smime.p7s\"\r\n\r\n" +
		b64(sig) + "--bnd--\r\n")
}

func trustFrom(pool *x509.CertPool) *Trust {
	t := &Trust{}
	t.pool = pool
	return t
}

func TestVerifyDetached(t *testing.T) {
	pki := newPKI(t, "signer@example.com")
	content := "Content-Type: text/plain\r\n\r\nhello signed world"
	sig := pki.sign(t, []byte(content), true)
	raw := detachedMessage(content, sig)

	res := Verify(raw, "signed", trustFrom(pki.pool), time.Now())
	if !res.Valid || res.Error != "" {
		t.Fatalf("expected valid, got %+v", res)
	}
	if !strings.Contains(res.Signer, "signer@example.com") {
		t.Fatalf("signer = %q", res.Signer)
	}

	// Tampered content must fail.
	tampered := detachedMessage(strings.Replace(content, "hello", "HELLO", 1), sig)
	if res := Verify(tampered, "signed", trustFrom(pki.pool), time.Now()); res.Valid {
		t.Fatal("tampered content verified")
	}

	// Untrusted CA must fail but still name the signer.
	other := newPKI(t, "evil@example.com")
	if res := Verify(raw, "signed", trustFrom(other.pool), time.Now()); res.Valid || res.Error == "" {
		t.Fatalf("untrusted CA accepted: %+v", res)
	}

	// No trust store: unverified with explanation.
	if res := Verify(raw, "signed", nil, time.Now()); res.Valid || !strings.Contains(res.Error, "trust store") {
		t.Fatalf("nil trust: %+v", res)
	}
}

func TestVerifyOpaque(t *testing.T) {
	pki := newPKI(t, "signer@example.com")
	inner := "Content-Type: text/plain\r\n\r\nopaque body"
	der := pki.sign(t, []byte(inner), false)
	raw := []byte("From: a@b\r\nTo: c@d\r\nSubject: o\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" + b64(der))
	res := Verify(raw, "signed-opaque", trustFrom(pki.pool), time.Now())
	if !res.Valid {
		t.Fatalf("expected valid: %+v", res)
	}
	if !strings.Contains(string(res.Content), "opaque body") {
		t.Fatalf("content = %q", res.Content)
	}
	// Expired at verification time.
	if res := Verify(raw, "signed-opaque", trustFrom(pki.pool), time.Now().Add(48*time.Hour)); res.Valid {
		t.Fatal("expired certificate accepted")
	}
}
