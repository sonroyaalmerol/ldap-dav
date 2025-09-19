package auth

import (
	"context"
	"errors"

	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"

	"github.com/rs/zerolog"
)

type Principal struct {
	UserID  string // uid
	UserDN  string
	Display string
	// More attrs if needed
}

type ctxKey int

const principalKey ctxKey = 1

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey).(*Principal)
	return p, ok
}

type Chain struct {
	cfg    *config.Config
	dir    directory.Directory
	logger zerolog.Logger
	basic  *BasicAuth
	bearer *BearerAuth
}

func NewChain(cfg *config.Config, dir directory.Directory, logger zerolog.Logger) *Chain {
	c := &Chain{
		cfg:    cfg,
		dir:    dir,
		logger: logger,
	}
	if cfg.Auth.EnableBasic {
		c.basic = &BasicAuth{Dir: dir, Logger: logger}
	}
	if cfg.Auth.EnableBearer {
		c.bearer = NewBearerAuth(cfg, dir, logger)
	}
	return c
}

func (c *Chain) BasicEnabled() bool  { return c.basic != nil }
func (c *Chain) BearerEnabled() bool { return c.bearer != nil }

func (c *Chain) BasicAuthenticate(ctx context.Context, header string) (*Principal, error) {
	if c.basic == nil {
		return nil, errors.New("basic disabled")
	}
	return c.basic.Authenticate(ctx, header)
}

func (c *Chain) BearerAuthenticate(ctx context.Context, token string) (*Principal, error) {
	if c.bearer == nil {
		return nil, errors.New("bearer disabled")
	}
	return c.bearer.Authenticate(ctx, token)
}
