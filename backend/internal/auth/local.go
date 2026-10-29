package auth

import "context"

// localOnly is the Authenticator for deployments without a directory: every
// login must match a local account (checked before the Authenticator is
// consulted), so this one rejects everything and cannot look users up.
type localOnly struct{}

func NewLocalOnly() Authenticator { return localOnly{} }

func (localOnly) Login(context.Context, string, string) (*Identity, error) {
	return nil, ErrInvalidCredentials
}

func (localOnly) Lookup(context.Context, string) (*Identity, error) { return nil, nil }
