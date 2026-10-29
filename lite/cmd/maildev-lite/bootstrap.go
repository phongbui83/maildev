package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/phongbui83/maildev/internal/secrets"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/config"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

const liteSlug = "default"

// ensureLite creates the one project (auto-join: every account becomes a
// member, admins owners) with credentials, and the first admin account when
// none exists: MAILDEV_LITE_ADMIN_USER / MAILDEV_LITE_ADMIN_PASSWORD, default
// admin / admin, with a forced password change at the first login.
func ensureLite(ctx context.Context, st *store.Store, cfg *config.Config) error {
	p, err := st.GetProjectBySlug(ctx, liteSlug)
	if errors.Is(err, store.ErrNotFound) {
		name := os.Getenv("MAILDEV_LITE_PROJECT_NAME")
		if name == "" {
			name = "Default"
		}
		p, err = st.CreateProject(ctx, store.CreateProjectParams{
			Slug: liteSlug, Name: name, Description: "The single project of this MailDev lite instance. Every account is a member.",
			AutoJoin: true, RetentionDays: 14, MaxEmails: 5000,
		})
		if err != nil {
			return err
		}
		slog.Info("created project", "slug", liteSlug)
		st.Audit(ctx, store.AuditParams{Actor: "system", Protocol: "system", ProjectID: &p.ID, Action: "project_create", Target: p.Slug})
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
		st.Audit(ctx, store.AuditParams{Actor: "system", Protocol: "system", ProjectID: &p.ID, Action: "credentials_init", Target: p.Slug})
	}

	if cfg.AuthMode != "local" {
		return nil // directory / dev mode: admins come from MAILDEV_ADMIN_USERS at login
	}
	n, err := st.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	user := strings.ToLower(strings.TrimSpace(os.Getenv("MAILDEV_LITE_ADMIN_USER")))
	if user == "" {
		user = "admin"
	}
	pw := os.Getenv("MAILDEV_LITE_ADMIN_PASSWORD")
	if pw == "" {
		pw = "admin"
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := st.CreateLocalUserWithRole(ctx, user, "Administrator", "", model.RoleAdmin, hash, true); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// The username exists but is not an admin: promote it rather than lock everyone out.
			u, gerr := st.GetUserByUsername(ctx, user)
			if gerr != nil {
				return gerr
			}
			if err := st.SetUserRole(ctx, u.ID, model.RoleAdmin); err != nil {
				return err
			}
			slog.Warn("promoted existing account to admin (no admin existed)", "user", user)
			return nil
		}
		return err
	}
	slog.Warn("created initial admin account; the password must be changed at first login", "user", user)
	st.Audit(ctx, store.AuditParams{Actor: "system", Protocol: "system", Action: "local_user_create", Target: user, Detail: map[string]any{"role": "admin", "bootstrap": true}})
	return nil
}

// resetAdmin (maildev-lite reset-admin <username> [password]) sets a new
// temporary password on an account, creating it with the admin role when it
// does not exist, and promotes it to admin. For a locked-out site; run with the
// service stopped or at least with the same /data volume:
//
//	docker compose run --rm maildev reset-admin admin
func resetAdmin(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: maildev-lite reset-admin <username> [password]")
	}
	user := strings.ToLower(strings.TrimSpace(args[0]))
	pw := ""
	if len(args) > 1 {
		pw = args[1]
	}
	generated := pw == ""
	if generated {
		pw = secrets.Password(16) + "!"
	}
	dataDir := os.Getenv("MAILDEV_DATA_DIR")
	if dataDir == "" {
		dataDir = firstDir("/data", "data")
	}
	key, err := masterKey(dataDir)
	if err != nil {
		return err
	}
	os.Setenv("MAILDEV_MASTER_KEY", key)
	setDefault("MAILDEV_EDITION", "lite")
	setDefault("MAILDEV_AUTH_MODE", "local")
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	box, err := secrets.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dataDir, "maildev.db"), box)
	if err != nil {
		return err
	}
	defer st.Close()
	st.SetLite(true)
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	u, err := st.GetUserByUsername(ctx, user)
	if errors.Is(err, store.ErrNotFound) {
		if u, err = st.CreateLocalUserWithRole(ctx, user, "Administrator", "", model.RoleAdmin, hash, true); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if err := st.SetPassword(ctx, u.ID, hash, true); err != nil {
			return err
		}
		if err := st.SetUserRole(ctx, u.ID, model.RoleAdmin); err != nil {
			return err
		}
		if err := st.SetUserDisabled(ctx, u.ID, false); err != nil {
			return err
		}
		_ = st.Unlock(ctx, u.ID)
	}
	st.Audit(ctx, store.AuditParams{Actor: "system", Protocol: "system", Action: "password_reset", Target: user, Detail: map[string]any{"cli": true, "generated_password": generated}})
	fmt.Printf("account %s is admin; temporary password: %s (must be changed at next login)\n", user, pw)
	return nil
}
