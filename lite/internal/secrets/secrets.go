// Package secrets provides credential generation and reversible encryption
// for project credentials that users must be able to read back from the UI.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
)

type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; the nonce is prepended to the ciphertext.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, b.aead.Seal(nil, nonce, plaintext, nil)...), nil
}

func (b *Box) Open(ciphertext []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], nil)
}

const (
	alnum    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	upperNum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	lowerNum = "abcdefghijklmnopqrstuvwxyz0123456789"
)

func randomString(n int, alphabet string) string {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(fmt.Sprintf("crypto/rand: %v", err))
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out)
}

// Password returns a random alphanumeric password suitable for SMTP/IMAP logins.
func Password(n int) string { return randomString(n, alnum) }

// KeyID returns a short lowercase identifier.
func KeyID(n int) string { return randomString(n, lowerNum) }

// AWSAccessKeyID mimics the shape of an AWS access key id so SDKs accept it.
func AWSAccessKeyID() string { return "AKIA" + randomString(16, upperNum) }

// AWSSecret mimics the shape of an AWS secret access key.
func AWSSecret() string { return randomString(40, alnum+"/+") }

// SessionToken returns a URL-safe random token.
func SessionToken() string {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
