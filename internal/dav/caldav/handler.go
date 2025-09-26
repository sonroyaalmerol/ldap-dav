package caldav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
		logger.Error().Err(err).Str("timezone", cfg.Timezone).Msg("failed to load timezone, using UTC")
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

func (h *Handlers) ensureSchedulingCollections(ctx context.Context, userID string) {
	if err := h.store.CreateSchedulingInbox(ctx, userID, ""); err != nil {
		h.logger.Error().Err(err).Str("user", userID).Msg("failed to create scheduling inbox")
	}

	if err := h.store.CreateSchedulingOutbox(ctx, userID, ""); err != nil {
		h.logger.Error().Err(err).Str("user", userID).Msg("failed to create scheduling outbox")
	}
}

func (h *Handlers) ensurePersonalCalendar(ctx context.Context, ownerUID string) {
	now := time.Now().UTC()
	calURI := fmt.Sprintf("personal-%s", ownerUID)
	cal := storage.Calendar{
		ID:          "",
		OwnerUserID: ownerUID,
		OwnerGroup:  "",
		URI:         calURI,
		DisplayName: "Personal Calendar",
		Description: "Personal Calendar",
		CTag:        "",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if existingCal, err := h.store.GetCalendarByURI(ctx, calURI); err != nil || existingCal == nil {
		if err := h.store.CreateCalendar(cal, "", "Personal Calendar"); err != nil {
			h.logger.Error().Err(err).
				Str("user", ownerUID).
				Str("calendar", calURI).
				Str("owner", ownerUID).
				Msg("Failed to create Personal Calendar")
		}
	}

	h.ensureSchedulingCollections(ctx, ownerUID)
}

func (h *Handlers) mustCanRead(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI, calOwner string) bool {
	if pr.UserID == calOwner {
		return true
	}
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("user", pr.UserID).
			Str("calendar", calURI).
			Str("owner", calOwner).
			Msg("ACL check failed")
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if !eff.CanRead() {
		h.logger.Debug().
			Str("user", pr.UserID).
			Str("calendar", calURI).
			Str("owner", calOwner).
			Msg("ACL read denied")
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handlers) loadCalendarByOwnerURI(ctx context.Context, ownerUID, calURI string) (*storage.Calendar, error) {
	if cal, err := h.store.GetCalendarByURI(ctx, calURI); err == nil && cal != nil {
		if cal.OwnerUserID == ownerUID {
			return cal, nil
		}
	}

	cals, err := h.store.ListCalendarsByOwnerUser(ctx, ownerUID)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", ownerUID).
			Str("calendar", calURI).
			Msg("failed to list calendars by owner")
		return nil, err
	}
	for _, c := range cals {
		if c.URI == calURI {
			return c, nil
		}
	}
	return nil, errors.New("not found")
}

func (h *Handlers) resolveCalendar(ctx context.Context, owner, calURI string) (string, string, error) {
	if calURI != "" && calURI != "shared" {
		if cal, err := h.store.GetCalendarByURI(ctx, calURI); err == nil && cal != nil {
			return cal.ID, cal.OwnerUserID, nil
		}

		if cal, err := h.loadCalendarByOwnerURI(ctx, owner, calURI); err == nil && cal != nil {
			return cal.ID, owner, nil
		}
	}
	h.logger.Debug().
		Str("owner", owner).
		Str("calendar", calURI).
		Msg("calendar not found in resolveCalendar")
	return "", "", errors.New("calendar not found")
}

func (h *Handlers) aclCheckRead(ctx context.Context, pr *auth.Principal, calURI, calOwner string) (bool, error) {
	if pr.UserID == calOwner {
		return true, nil
	}
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("user", pr.UserID).
			Str("calendar", calURI).
			Str("owner", calOwner).
			Msg("ACL effective check failed")
		return false, err
	}
	return eff.CanRead(), nil
}
