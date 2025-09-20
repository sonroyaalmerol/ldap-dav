package dav

import (
	"context"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/caldav"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"

	"github.com/rs/zerolog"
)

type Handlers struct {
	cfg            *config.Config
	store          storage.Store
	dir            directory.Directory
	auth           *auth.Chain
	aclProv        acl.Provider
	logger         zerolog.Logger
	basePath       string
	CalDAVHandlers caldav.Handlers
}

func NewHandlers(cfg *config.Config, store storage.Store, dir directory.Directory, authn *auth.Chain, logger zerolog.Logger) *Handlers {
	return &Handlers{
		cfg:            cfg,
		store:          store,
		dir:            dir,
		auth:           authn,
		aclProv:        acl.NewLDAPACL(dir),
		logger:         logger,
		basePath:       cfg.HTTP.BasePath,
		CalDAVHandlers: *caldav.NewHandlers(cfg, store, dir, logger),
	}
}

func (h *Handlers) currentUserPrincipalHref(ctx context.Context) string {
	u, _ := common.CurrentUser(ctx)
	if u == nil {
		return common.JoinURL(h.basePath, "principals")
	}
	return common.PrincipalURL(h.basePath, u.UID)
}
