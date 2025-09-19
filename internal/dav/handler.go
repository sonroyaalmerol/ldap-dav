package dav

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"

	"github.com/rs/zerolog"
)

type Handlers struct {
	cfg      *config.Config
	store    storage.Store
	dir      directory.Directory
	auth     *auth.Chain
	aclProv  acl.Provider
	logger   zerolog.Logger
	basePath string
}

func NewHandlers(cfg *config.Config, store storage.Store, dir directory.Directory, authn *auth.Chain, logger zerolog.Logger) *Handlers {
	return &Handlers{
		cfg:      cfg,
		store:    store,
		dir:      dir,
		auth:     authn,
		aclProv:  acl.NewLDAPACL(dir),
		logger:   logger,
		basePath: cfg.HTTP.BasePath,
	}
}

func (h *Handlers) principalURL(uid string) string {
	return joinURL(h.basePath, "principals", "users", uid)
}

func (h *Handlers) calendarHome(uid string) string {
	return joinURL(h.basePath, "calendars", uid) + "/"
}

func (h *Handlers) calendarPath(ownerUID, calURI string) string {
	return joinURL(h.basePath, "calendars", ownerUID, calURI) + "/"
}

func (h *Handlers) sharedRoot(uid string) string {
	return joinURL(h.basePath, "calendars", uid, "shared") + "/"
}

func (h *Handlers) currentUser(ctx context.Context) (*directory.User, *auth.Principal) {
	pr, ok := auth.PrincipalFrom(ctx)
	if !ok || pr == nil {
		return nil, nil
	}
	return &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, pr
}

func mustPrincipal(ctx context.Context) *auth.Principal {
	pr, _ := auth.PrincipalFrom(ctx)
	return pr
}

// mustCanRead enforces read ACL and writes 403 if denied.
func (h *Handlers) mustCanRead(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI, calOwner string) bool {
	if pr.UserID == calOwner {
		return true
	}
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil || !eff.CanRead() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handlers) aclCheckRead(ctx context.Context, pr *auth.Principal, calURI, calOwner string) (bool, error) {
	if pr.UserID == calOwner {
		return true, nil
	}
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil {
		return false, err
	}
	return eff.CanRead(), nil
}

func (h *Handlers) splitCalendarPath(p string) (owner string, cal string, rest []string) {
	// Accept both absolute and full-URL hrefs
	if !strings.HasPrefix(p, "/") {
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			// HREF may be full URL; extract path
			if idx := strings.Index(p, "://"); idx >= 0 {
				if slash := strings.Index(p[idx+3:], "/"); slash >= 0 {
					p = p[idx+3+slash:]
				}
			}
		}
	}
	pp := p
	pp = strings.TrimPrefix(pp, h.basePath)
	pp = strings.TrimPrefix(pp, "/")
	parts := strings.Split(pp, "/")
	// patterns:
	// calendars/{owner}/ -> home
	// calendars/{owner}/{cal}/...
	if len(parts) == 0 {
		return "", "", nil
	}
	if parts[0] != "calendars" {
		return "", "", nil
	}
	if len(parts) == 2 {
		// home
		return parts[1], "", nil
	}
	if len(parts) >= 3 {
		return parts[1], parts[2], parts[3:]
	}
	return "", "", nil
}

func (h *Handlers) loadCalendarByOwnerURI(ctx context.Context, ownerUID, calURI string) (*storage.Calendar, error) {
	cals, err := h.store.ListCalendarsByOwnerUser(ctx, ownerUID)
	if err != nil {
		return nil, err
	}
	for _, c := range cals {
		if c.URI == calURI {
			return c, nil
		}
	}
	return nil, errors.New("not found")
}

// resolveCalendar resolves a path which might be an owned calendar or a shared synthetic mount.
// Returns calendarID and canonical owner UID.
func (h *Handlers) resolveCalendar(ctx context.Context, owner, calURI string) (string, string, error) {
	// Owned calendar?
	if calURI != "" && calURI != "shared" {
		if cal, err := h.loadCalendarByOwnerURI(ctx, owner, calURI); err == nil && cal != nil {
			return cal.ID, owner, nil
		}
		// If shared mount: look up any calendar with this URI in store, as shared reference.
		if cal, err := h.findCalendarByURI(ctx, calURI); err == nil && cal != nil {
			return cal.ID, cal.OwnerUserID, nil
		}
	}
	return "", "", errors.New("calendar not found")
}

func (h *Handlers) findCalendarByURI(ctx context.Context, uri string) (*storage.Calendar, error) {
	all, err := h.store.ListAllCalendars(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if c.URI == uri {
			return c, nil
		}
	}
	return nil, errors.New("not found")
}

func (h *Handlers) ownerPrincipalForCalendar(c *storage.Calendar) string {
	if c.OwnerUserID != "" {
		return h.principalURL(c.OwnerUserID)
	}
	// could be group-owned; expose group principal path if implemented
	return joinURL(h.basePath, "principals")
}

func (h *Handlers) buildReadOnlyACL(r *http.Request, calURI, ownerUID string) *aclProp {
	pr := mustPrincipal(r.Context())
	if pr == nil {
		return nil
	}

	isOwner := pr.UserID == ownerUID
	var eff struct{ Read, WP, WC, B, U bool }
	if isOwner {
		eff = struct{ Read, WP, WC, B, U bool }{true, true, true, true, true}
	} else {
		e, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			return nil
		}
		eff = struct{ Read, WP, WC, B, U bool }{e.CanRead(), e.WriteProps, e.WriteContent, e.Bind, e.Unbind}
	}
	g := grant{}
	p := priv{}
	if eff.Read {
		p.Read = &struct{}{}
	}
	if eff.WP {
		p.WriteProps = &struct{}{}
	}
	if eff.WC {
		p.WriteContent = &struct{}{}
	}
	if eff.B {
		p.Bind = &struct{}{}
	}
	if eff.U {
		p.Unbind = &struct{}{}
	}
	if p.Read != nil || p.WriteProps != nil || p.WriteContent != nil || p.Bind != nil || p.Unbind != nil {
		g.Privs = append(g.Privs, p)
	}
	return &aclProp{
		ACE: []ace{
			{
				Principal: href{Value: h.principalURL(pr.UserID)},
				Grant:     g,
			},
		},
	}
}

