package caldav

import (
	"context"
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (h *Handlers) checkReadAccess(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI string) bool {
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil || !eff.Read {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handlers) checkWriteAccess(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI string, existing *storage.Object) bool {
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}

	if existing == nil {
		if !eff.Bind {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
	} else {
		if !eff.WriteContent {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
	}
	return true
}

func (h *Handlers) checkUnbindAccess(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI string) bool {
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil || !eff.Unbind {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handlers) checkBindAccess(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI string) bool {
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil || !eff.Bind {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handlers) checkWritePropsAccess(w http.ResponseWriter, ctx context.Context, pr *auth.Principal, calURI string) bool {
	eff, err := h.aclProv.Effective(ctx, &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
	if err != nil || !eff.WriteProps {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}
