package carddav

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

type Handlers struct {
	cfg             *config.Config
	store           storage.Store
	aclProv         acl.Provider
	logger          zerolog.Logger
	basePath        string
	dir             directory.Directory
	addressbookDirs map[string]directory.ContactDirectory
}

func NewHandlers(cfg *config.Config, store storage.Store, dir directory.Directory, logger zerolog.Logger) *Handlers {
	addressbookDirs := make(map[string]directory.ContactDirectory)
	for _, f := range cfg.LDAP.AddressbookFilters {
		if !f.Enabled {
			continue
		}
		client, err := directory.NewLDAPContactClient(f, cfg.LDAP)
		if err != nil {
			logger.Error().Err(err).
				Str("url", f.URL).
				Str("binddn", f.BindDN).
				Msg("Failed to init LDAP contact client for addressbook")
			continue
		}
		addressbookDirs["ldap_"+f.URI] = client
	}

	return &Handlers{
		cfg:             cfg,
		store:           store,
		dir:             dir,
		aclProv:         acl.NewLDAPACL(dir),
		logger:          logger,
		basePath:        cfg.HTTP.BasePath,
		addressbookDirs: addressbookDirs,
	}
}

func (h *Handlers) ensurePersonalAddressbook(ctx context.Context, ownerUID string) {
	abURI := fmt.Sprintf("personal-%s", ownerUID)
	ab := storage.Addressbook{
		ID:          "",
		OwnerUserID: ownerUID,
		OwnerGroup:  "",
		URI:         abURI,
		DisplayName: "Personal Address Book",
		Description: "Personal Address Book",
		CTag:        "",
	}

	if existingAB, err := h.store.GetAddressbookByURI(ctx, abURI); err != nil || existingAB == nil {
		if err := h.store.CreateAddressbook(ab, "", "Personal Address Book"); err != nil {
			h.logger.Error().Err(err).
				Str("user", ownerUID).
				Str("addressbook", abURI).
				Str("owner", ownerUID).
				Msg("Failed to create Personal Address Book")
		}
	}
}

func (h *Handlers) mustCanRead(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, abURI, abOwner string) bool {
	if pr.UserID == abOwner {
		return true
	}
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("user", pr.UserID).
			Str("addressbook", abURI).
			Str("owner", abOwner).
			Msg("ACL check failed")
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if !eff.CanRead() {
		h.logger.Debug().
			Str("user", pr.UserID).
			Str("addressbook", abURI).
			Str("owner", abOwner).
			Msg("ACL read denied")
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handlers) loadAddressbookByOwnerURI(ctx context.Context, ownerUID, abURI string) (*storage.Addressbook, error) {
	if ab, err := h.store.GetAddressbookByURI(ctx, abURI); err == nil && ab != nil {
		if ab.OwnerUserID == ownerUID {
			return ab, nil
		}
	}

	addressbooks, err := h.store.ListAddressbooksByOwnerUser(ctx, ownerUID)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", ownerUID).
			Str("addressbook", abURI).
			Msg("failed to list addressbooks by owner")
		return nil, err
	}
	for _, ab := range addressbooks {
		if ab.URI == abURI {
			return ab, nil
		}
	}
	return nil, errors.New("not found")
}

func (h *Handlers) resolveAddressbook(ctx context.Context, owner, abURI string) (string, string, error) {
	if strings.HasPrefix(abURI, "ldap_") {
		if _, ok := h.addressbookDirs[abURI]; ok {
			return abURI, owner, nil
		}
		return "", "", errors.New("addressbook not found")
	}

	if abURI != "" && abURI != "shared" {
		if ab, err := h.store.GetAddressbookByURI(ctx, abURI); err == nil && ab != nil {
			return ab.ID, ab.OwnerUserID, nil
		}

		if ab, err := h.loadAddressbookByOwnerURI(ctx, owner, abURI); err == nil && ab != nil {
			return ab.ID, owner, nil
		}
	}
	h.logger.Debug().
		Str("owner", owner).
		Str("addressbook", abURI).
		Msg("addressbook not found in resolveAddressbook")
	return "", "", errors.New("addressbook not found")
}

func (h *Handlers) aclCheckRead(ctx context.Context, pr *auth.Principal, abURI, abOwner string) (bool, error) {
	if pr.UserID == abOwner {
		return true, nil
	}
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("user", pr.UserID).
			Str("addressbook", abURI).
			Str("owner", abOwner).
			Msg("ACL effective check failed")
		return false, err
	}
	return eff.CanRead(), nil
}

func (h *Handlers) addressbookExists(ctx context.Context, owner, uri string) bool {
	ab, err := h.store.GetAddressbookByURI(ctx, uri)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", uri).
			Msg("failed to check if addressbook exists")
		return false
	}
	return ab != nil && ab.OwnerUserID == owner
}
