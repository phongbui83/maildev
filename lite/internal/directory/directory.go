// Package directory serves the company user list (https://directory.example.com/users/)
// so that owners can pick members from a dropdown instead of typing usernames.
// The list is cached in memory and refreshed in the background.
package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type User struct {
	Username   string `json:"username"`
	FullName   string `json:"fullname"`
	Email      string `json:"email"`
	Department string `json:"department"`
}

type Directory struct {
	url    string
	client *http.Client
	// OnLoaded is called after every successful refresh (e.g. to import users).
	OnLoaded func(users []User)

	mu        sync.RWMutex
	users     []User
	byName    map[string]User
	fetchedAt time.Time
}

func New(url string) *Directory {
	return &Directory{url: url, client: &http.Client{Timeout: 20 * time.Second}, byName: map[string]User{}}
}

func (d *Directory) Enabled() bool { return d != nil && d.url != "" }

// Ready reports whether the list has been loaded at least once.
func (d *Directory) Ready() bool { return d.Enabled() && d.Size() > 0 }

// Refresh downloads the list. The previous list is kept on failure.
func (d *Directory) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("directory: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var raw []struct {
		Username   string `json:"username"`
		FullName   string `json:"fullname"`
		Email      string `json:"email"`
		Department string `json:"department"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		// Some deployments wrap the list in an object.
		var wrapped map[string]json.RawMessage
		if json.Unmarshal(body, &wrapped) != nil {
			return fmt.Errorf("directory: unexpected JSON")
		}
		for _, k := range []string{"users", "data", "results", "items"} {
			if v, ok := wrapped[k]; ok && json.Unmarshal(v, &raw) == nil {
				break
			}
		}
	}
	users := make([]User, 0, len(raw))
	byName := make(map[string]User, len(raw))
	for _, r := range raw {
		u := User{Username: strings.ToLower(strings.TrimSpace(r.Username)), FullName: strings.TrimSpace(r.FullName), Email: strings.TrimSpace(r.Email), Department: strings.TrimSpace(r.Department)}
		if u.Username == "" {
			continue
		}
		users = append(users, u)
		byName[u.Username] = u
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	d.mu.Lock()
	d.users, d.byName, d.fetchedAt = users, byName, time.Now()
	d.mu.Unlock()
	slog.Info("directory loaded", "users", len(users), "url", d.url)
	if d.OnLoaded != nil {
		d.OnLoaded(users)
	}
	return nil
}

// RefreshLoop keeps the cache fresh until ctx ends. While the list has never
// been loaded it retries every 30 seconds instead of waiting the full interval.
func (d *Directory) RefreshLoop(ctx context.Context, every time.Duration) {
	for {
		wait := every
		if !d.Ready() {
			wait = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			if err := d.Refresh(ctx); err != nil {
				slog.Warn("directory refresh failed", "err", err)
			}
		}
	}
}

// All returns a copy of the cached list.
func (d *Directory) All() []User {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]User(nil), d.users...)
}

// Search returns up to limit users whose username, name, email or department
// contain q (case-insensitive). Empty q lists the first entries.
func (d *Directory) Search(q string, limit int) []User {
	d.mu.RLock()
	defer d.mu.RUnlock()
	q = strings.ToLower(strings.TrimSpace(q))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]User, 0, limit)
	// Prefix matches on username first, then substring matches anywhere.
	for pass := 0; pass < 2 && len(out) < limit; pass++ {
		for _, u := range d.users {
			if len(out) >= limit {
				break
			}
			if q == "" {
				if pass == 0 {
					out = append(out, u)
				}
				continue
			}
			prefix := strings.HasPrefix(u.Username, q)
			sub := strings.Contains(u.Username, q) || strings.Contains(strings.ToLower(u.FullName), q) || strings.Contains(strings.ToLower(u.Email), q) || strings.Contains(strings.ToLower(u.Department), q)
			if (pass == 0 && prefix) || (pass == 1 && sub && !prefix) {
				out = append(out, u)
			}
		}
	}
	return out
}

func (d *Directory) Lookup(username string) (User, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	u, ok := d.byName[strings.ToLower(username)]
	return u, ok
}

func (d *Directory) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.users)
}
