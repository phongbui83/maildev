// Package bootstrap seeds the demo project and runs periodic housekeeping.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/phongbui83/maildev/internal/store"
)

const DemoSlug = "demo"

// EnsureDemoProject creates the auto-join "demo" project with credentials on
// first start. Every user becomes a member at login; admins become owners.
func EnsureDemoProject(ctx context.Context, st *store.Store) error {
	p, err := st.GetProjectBySlug(ctx, DemoSlug)
	if errors.Is(err, store.ErrNotFound) {
		p, err = st.CreateProject(ctx, store.CreateProjectParams{
			Slug:          DemoSlug,
			Name:          "Demo",
			Description:   "Shared sandbox: everyone who logs in is a member. Do not send sensitive data here.",
			AutoJoin:      true,
			RetentionDays: 14,
			MaxEmails:     1000,
		})
		if err != nil {
			return err
		}
		slog.Info("created demo project", "id", p.ID)
		st.Audit(ctx, store.AuditParams{Actor: "system", ProjectID: &p.ID, Action: "project_create", Target: p.Slug})
	} else if err != nil {
		return err
	}
	creds, err := st.ListActiveCredentials(ctx, p.ID)
	if err != nil {
		return err
	}
	if len(creds) == 0 {
		if _, err := st.InitCredentials(ctx, p, nil); err != nil {
			return err
		}
		slog.Info("initialized demo project credentials")
		st.Audit(ctx, store.AuditParams{Actor: "system", ProjectID: &p.ID, Action: "credentials_init", Target: p.Slug})
	}
	return nil
}

// Housekeeping applies retention and purges expired sessions on an interval.
func Housekeeping(ctx context.Context, st *store.Store, every time.Duration) {
	run := func() {
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if n, err := st.ApplyRetention(c); err != nil {
			slog.Error("retention failed", "err", err)
		} else if n > 0 {
			slog.Info("retention removed emails", "count", n)
			st.Audit(c, store.AuditParams{Actor: "system", Action: "retention_cleanup", Protocol: "system", Detail: map[string]any{"deleted": n}})
		}
		if err := st.PurgeExpiredSessions(c); err != nil {
			slog.Error("session purge failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
