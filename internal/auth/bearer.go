package auth

import (
	"context"
	"errors"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/cache"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/rs/zerolog"
)

type BearerAuth struct {
	cfg    *config.Config
	Dir    directory.Directory
	Logger zerolog.Logger

	keyset jwk.Set
	ksAt   time.Time
	ksTTL  time.Duration

	verCache *cache.Cache[string, *Principal]
}

func NewBearerAuth(cfg *config.Config, dir directory.Directory, logger zerolog.Logger) *BearerAuth {
	return &BearerAuth{
		cfg:      cfg,
		Dir:      dir,
		Logger:   logger,
		ksTTL:    10 * time.Minute,
		verCache: cache.New[string, *Principal](2 * time.Minute),
	}
}

func (b *BearerAuth) Authenticate(ctx context.Context, token string) (*Principal, error) {
	if p, ok := b.verCache.Get(token); ok && p != nil {
		return p, nil
	}

	if b.cfg.Auth.JWKSURL == "" && !b.cfg.Auth.AllowOpaque {
		return nil, errors.New("no jwt validation configured")
	}

	// Try JWT first
	if b.cfg.Auth.JWKSURL != "" {
		// Fetch/cached JWKS
		set := b.keyset
		var err error
		if set == nil || time.Since(b.ksAt) > b.ksTTL {
			set, err = jwk.Fetch(ctx, b.cfg.Auth.JWKSURL)
			if err != nil {
				return nil, err
			}
			b.keyset = set
			b.ksAt = time.Now()
		}

		tok, err := jwt.Parse([]byte(token), jwt.WithKeySet(set), jwt.WithValidate(true))
		if err == nil {
			if iss := tok.Issuer(); b.cfg.Auth.Issuer != "" && iss != b.cfg.Auth.Issuer {
				return nil, errors.New("issuer mismatch")
			}
			if aud := tok.Audience(); len(aud) > 0 && b.cfg.Auth.Audience != "" {
				found := false
				for _, a := range aud {
					if a == b.cfg.Auth.Audience {
						found = true
						break
					}
				}
				if !found {
					return nil, errors.New("audience mismatch")
				}
			}
			sub := tok.Subject()
			if sub == "" {
				return nil, errors.New("no sub")
			}
			// Map token subject to LDAP user
			user, err := b.Dir.LookupUserByAttr(ctx, b.cfg.LDAP.TokenUserAttr, sub)
			if err != nil {
				return nil, err
			}
			p := &Principal{UserID: user.UID, UserDN: user.DN, Display: user.DisplayName}
			b.verCache.Set(token, p, time.Now().Add(2*time.Minute))
			return p, nil
		}
	}

	// Opaque introspection (optional)
	if b.cfg.Auth.AllowOpaque && b.cfg.Auth.IntrospectURL != "" {
		valid, sub, err := b.Dir.IntrospectToken(ctx, token, b.cfg.Auth.IntrospectURL, b.cfg.Auth.IntrospectAuthHeader)
		if err != nil || !valid {
			return nil, errors.New("invalid token")
		}
		user, err := b.Dir.LookupUserByAttr(ctx, b.cfg.LDAP.TokenUserAttr, sub)
		if err != nil {
			return nil, err
		}
		return &Principal{UserID: user.UID, UserDN: user.DN, Display: user.DisplayName}, nil
	}

	return nil, errors.New("bearer rejected")
}
