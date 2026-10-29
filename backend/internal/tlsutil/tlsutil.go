// Package tlsutil loads the server certificate (reloading it when cert-manager
// rotates the files) or generates a self-signed one for local development.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log/slog"
	"math/big"
	"os"
	"sync"
	"time"
)

type reloader struct {
	certFile, keyFile string
	mu                sync.RWMutex
	cert              *tls.Certificate
	loadedAt          time.Time
	modTime           time.Time
}

func (r *reloader) load() error {
	ci, err := os.Stat(r.certFile)
	if err != nil {
		return err
	}
	ki, err := os.Stat(r.keyFile)
	if err != nil {
		return err
	}
	mt := ci.ModTime()
	if ki.ModTime().After(mt) {
		mt = ki.ModTime()
	}
	r.mu.RLock()
	same := r.cert != nil && mt.Equal(r.modTime)
	r.mu.RUnlock()
	if same {
		return nil
	}
	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.cert, r.modTime, r.loadedAt = &c, mt, time.Now()
	r.mu.Unlock()
	slog.Info("loaded TLS certificate", "cert", r.certFile)
	return nil
}

func (r *reloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	stale := time.Since(r.loadedAt) > time.Minute
	c := r.cert
	r.mu.RUnlock()
	if stale {
		if err := r.load(); err != nil {
			slog.Warn("TLS certificate reload failed, keeping previous", "err", err)
		}
		r.mu.RLock()
		c = r.cert
		r.mu.RUnlock()
	}
	return c, nil
}

// Config returns a TLS config backed by certFile/keyFile when both are set,
// otherwise by a freshly generated self-signed certificate for hostname.
func Config(certFile, keyFile, hostname string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile != "" && keyFile != "" {
		r := &reloader{certFile: certFile, keyFile: keyFile}
		if err := r.load(); err != nil {
			return nil, err
		}
		cfg.GetCertificate = r.get
		return cfg, nil
	}
	slog.Warn("no TLS certificate configured, generating a self-signed certificate", "hostname", hostname)
	cert, err := selfSigned(hostname)
	if err != nil {
		return nil, err
	}
	cfg.Certificates = []tls.Certificate{cert}
	return cfg, nil
}

func selfSigned(hostname string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hostname, Organization: []string{"MailDev (self-signed)"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{hostname, "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
