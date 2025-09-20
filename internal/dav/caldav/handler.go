package caldav

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

type Handlers struct {
	cfg      *config.Config
	store    storage.Store
	aclProv  acl.Provider
	logger   zerolog.Logger
	basePath string
	expander *ical.RecurrenceExpander
}

func NewHandlers(cfg *config.Config, store storage.Store, dir directory.Directory, logger zerolog.Logger) *Handlers {
	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		tz = time.UTC
	}

	return &Handlers{
		cfg:      cfg,
		store:    store,
		aclProv:  acl.NewLDAPACL(dir),
		logger:   logger,
		basePath: cfg.HTTP.BasePath,
		expander: ical.NewRecurrenceExpander(tz),
	}
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

func (h *Handlers) SplitCalendarPath(p string) (owner string, cal string, rest []string) {
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
		// /calendars/{owner}/ (home)
		return parts[1], "", nil
	}

	// Shared normalization:
	// /calendars/{owner}/shared/{targetCalURI}/...
	if len(parts) >= 4 && parts[2] == "shared" {
		return parts[1], parts[3], parts[4:]
	}

	if len(parts) >= 3 {
		// Owned calendar
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
