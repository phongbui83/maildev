package relay

import (
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/phongbui83/maildev/internal/model"
)

// Signing-key derivation example from the AWS SigV4 documentation.
func TestSigV4SigningKey(t *testing.T) {
	secret := "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	kDate := hmacSHA256([]byte("AWS4"+secret), "20150830")
	kRegion := hmacSHA256(kDate, "us-east-1")
	kService := hmacSHA256(kRegion, "iam")
	kSigning := hmacSHA256(kService, "aws4_request")
	got := hex.EncodeToString(kSigning)
	want := "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	if got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

func TestSignV4Header(t *testing.T) {
	body := []byte(`{"a":1}`)
	req, _ := http.NewRequest(http.MethodPost, "https://email.ap-southeast-1.amazonaws.com/v2/email/outbound-emails", strings.NewReader(string(body)))
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	signV4(req, body, "AKIDEXAMPLE", "secret", "ap-southeast-1", "ses", now)

	auth := req.Header.Get("Authorization")
	re := regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261029/ap-southeast-1/ses/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=[0-9a-f]{64}$`)
	if !re.MatchString(auth) {
		t.Fatalf("unexpected Authorization header: %s", auth)
	}
	if req.Header.Get("X-Amz-Date") != "20261029T100000Z" {
		t.Fatalf("x-amz-date = %s", req.Header.Get("X-Amz-Date"))
	}
	if req.Header.Get("X-Amz-Content-Sha256") != sha256hex(body) {
		t.Fatal("payload hash header mismatch")
	}
	// Deterministic: same input, same signature.
	req2, _ := http.NewRequest(http.MethodPost, req.URL.String(), strings.NewReader(string(body)))
	signV4(req2, body, "AKIDEXAMPLE", "secret", "ap-southeast-1", "ses", now)
	if req2.Header.Get("Authorization") != auth {
		t.Fatal("signature is not deterministic")
	}
}

func TestCheckRecipients(t *testing.T) {
	cfg := RelayConfigForTest("*@example.com, qa-*@example.com")
	if err := CheckRecipients(cfg, []string{"a@example.com", "qa-1@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckRecipients(cfg, []string{"a@example.com", "x@gmail.com"}); err == nil {
		t.Fatal("expected rejection")
	}
	if err := CheckRecipients(RelayConfigForTest(""), []string{"anyone@anywhere"}); err != nil {
		t.Fatal("empty allow-list should allow everyone")
	}
}

func RelayConfigForTest(allowed string) *model.RelayConfig {
	return &model.RelayConfig{Kind: "smtp", AllowedRecipients: allowed}
}
