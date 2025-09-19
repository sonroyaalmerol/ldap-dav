package auth

import (
	"context"
	"net/http"
	"strings"

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

func (c *Chain) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		var p *Principal
		var err error

		if authz != "" && strings.HasPrefix(strings.ToLower(authz), "bearer ") && c.bearer != nil {
			p, err = c.bearer.Authenticate(r.Context(), strings.TrimSpace(authz[7:]))
		} else if authz != "" && strings.HasPrefix(strings.ToLower(authz), "basic ") && c.basic != nil {
			p, err = c.basic.Authenticate(r.Context(), authz)
		} else if c.basic != nil {
			// allow Basic via browser dialog
			p, err = c.basic.Authenticate(r.Context(), authz)
		}

		if err != nil || p == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="CalDAV", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}
