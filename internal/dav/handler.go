package dav

import (
	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/caldav"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"

	"github.com/rs/zerolog"
)

type Handlers struct {
	cfg              *config.Config
	store            storage.Store
	dir              directory.Directory
	auth             *auth.Chain
	aclProv          acl.Provider
	logger           zerolog.Logger
	basePath         string
	CalDAVHandlers   caldav.Handlers
	resourceHandlers map[string]ResourceHandler
}

var _ ResourceHandler = (*caldav.CalDAVResourceHandler)(nil)

//var _ ResourceHandler = (*caldav.CardDAVResourceHandler)(nil)

func NewHandlers(cfg *config.Config, store storage.Store, dir directory.Directory, authn *auth.Chain, logger zerolog.Logger) *Handlers {
	h := &Handlers{
		cfg:            cfg,
		store:          store,
		dir:            dir,
		auth:           authn,
		aclProv:        acl.NewLDAPACL(dir),
		logger:         logger,
		basePath:       cfg.HTTP.BasePath,
		CalDAVHandlers: *caldav.NewHandlers(cfg, store, dir, logger),
	}

	h.RegisterResourceHandler("calendars", caldav.NewCalDAVResourceHandler(&h.CalDAVHandlers, h.basePath))
	//h.RegisterResourceHandler("addressbooks", caldav.NewCardDAVResourceHandler(&h.CardDAVHandlers, h.basePath))

	return h
}
