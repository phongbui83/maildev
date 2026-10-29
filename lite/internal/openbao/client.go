// Package openbao is a small client for the OpenBao/Vault HTTP API: AppRole or
// token authentication with renewal, and the Transit engine operations MailDev
// needs to wrap per-project data keys.
package openbao

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Addr         string // e.g. http://openbao-active.openbao.svc:8200
	Token        string // static token (optional)
	RoleID       string // AppRole (optional, preferred)
	SecretID     string
	TransitMount string // default "transit"
	KeyPrefix    string // default "maildev-"
}

type Client struct {
	cfg  Config
	http *http.Client

	mu       sync.Mutex
	token    string
	expires  time.Time
	renewTTL time.Duration
}

var ErrNotFound = errors.New("openbao: not found")

func New(cfg Config) (*Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("openbao address is required")
	}
	if cfg.TransitMount == "" {
		cfg.TransitMount = "transit"
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "maildev-"
	}
	if cfg.Token == "" && (cfg.RoleID == "" || cfg.SecretID == "") {
		return nil, errors.New("openbao token or approle role_id+secret_id is required")
	}
	c := &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}, token: cfg.Token}
	return c, nil
}

type apiError struct {
	Status int
	Errors []string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("openbao: http %d: %s", e.Status, strings.Join(e.Errors, "; "))
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any, auth bool) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.Addr, "/")+"/v1/"+strings.TrimLeft(path, "/"), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		tok, err := c.ensureToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("X-Vault-Token", tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("openbao: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == 404 {
		return ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		var ae struct {
			Errors []string `json:"errors"`
		}
		_ = json.Unmarshal(data, &ae)
		return &apiError{Status: resp.StatusCode, Errors: ae.Errors}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ensureToken logs in with AppRole when needed and renews before expiry.
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.RoleID == "" {
		return c.token, nil // static token
	}
	if c.token != "" && time.Until(c.expires) > c.renewTTL/4 {
		return c.token, nil
	}
	if c.token != "" {
		// Try to renew first.
		var out struct {
			Auth struct {
				ClientToken   string `json:"client_token"`
				LeaseDuration int    `json:"lease_duration"`
			} `json:"auth"`
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.Addr, "/")+"/v1/auth/token/renew-self", nil)
		req.Header.Set("X-Vault-Token", c.token)
		if resp, err := c.http.Do(req); err == nil {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 && json.Unmarshal(data, &out) == nil && out.Auth.LeaseDuration > 0 {
				c.renewTTL = time.Duration(out.Auth.LeaseDuration) * time.Second
				c.expires = time.Now().Add(c.renewTTL)
				return c.token, nil
			}
		}
	}
	var out struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	body := map[string]string{"role_id": c.cfg.RoleID, "secret_id": c.cfg.SecretID}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.Addr, "/")+"/v1/auth/approle/login", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openbao approle login: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("openbao approle login: http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Auth.ClientToken == "" {
		return "", errors.New("openbao approle login: no token in response")
	}
	c.token = out.Auth.ClientToken
	c.renewTTL = time.Duration(out.Auth.LeaseDuration) * time.Second
	if c.renewTTL <= 0 {
		c.renewTTL = time.Hour
	}
	c.expires = time.Now().Add(c.renewTTL)
	slog.Info("openbao approle login ok", "ttl", c.renewTTL)
	return c.token, nil
}

func (c *Client) keyName(project string) string { return c.cfg.KeyPrefix + project }

// EnsureKey creates the project's transit key if it does not exist.
func (c *Client) EnsureKey(ctx context.Context, project string) error {
	path := c.cfg.TransitMount + "/keys/" + c.keyName(project)
	err := c.do(ctx, http.MethodGet, path, nil, nil, true)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"type": "aes256-gcm96"}, nil, true); err != nil {
		return err
	}
	// Allow crypto-shredding when the project is deleted.
	return c.do(ctx, http.MethodPost, path+"/config", map[string]any{"deletion_allowed": true}, nil, true)
}

// DeleteKey destroys the project's key; data encrypted under it becomes unreadable.
func (c *Client) DeleteKey(ctx context.Context, project string) error {
	err := c.do(ctx, http.MethodDelete, c.cfg.TransitMount+"/keys/"+c.keyName(project), nil, nil, true)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Wrap encrypts a data key with the project's transit key.
func (c *Client) Wrap(ctx context.Context, project string, dek []byte) (string, error) {
	var out struct {
		Data struct {
			Ciphertext string `json:"ciphertext"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, c.cfg.TransitMount+"/encrypt/"+c.keyName(project),
		map[string]string{"plaintext": base64.StdEncoding.EncodeToString(dek)}, &out, true)
	if err != nil {
		return "", err
	}
	return out.Data.Ciphertext, nil
}

// Unwrap decrypts a wrapped data key.
func (c *Client) Unwrap(ctx context.Context, project, ciphertext string) ([]byte, error) {
	var out struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	err := c.do(ctx, http.MethodPost, c.cfg.TransitMount+"/decrypt/"+c.keyName(project), map[string]string{"ciphertext": ciphertext}, &out, true)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Data.Plaintext)
}

// Ping verifies connectivity and credentials.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "auth/token/lookup-self", nil, nil, true)
}
