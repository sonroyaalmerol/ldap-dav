package dav

import (
	"io"
	"net/http"
	"path"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func (h *Handlers) HandlePropfind(w http.ResponseWriter, r *http.Request) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "0"
	}

	_, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()

	// Identify resource kind based on path
	if common.IsPrincipalPath(r.URL.Path, h.basePath) {
		h.propfindPrincipal(w, r, depth)
		return
	}

	if common.IsCalendarPath(r.URL.Path, h.basePath) {
		if owner, cal, rest := h.CalDAVHandlers.SplitCalendarPath(r.URL.Path); owner != "" {
			if len(rest) == 0 {
				// calendar collection or home
				if cal == "" {
					// home set listing calendars
					h.CalDAVHandlers.PropfindCalendarHome(w, r, owner, depth)
					return
				}
				// calendar collection
				h.CalDAVHandlers.PropfindCalendarCollection(w, r, owner, cal, depth)
				return
			}
			// object PROPFIND
			h.CalDAVHandlers.PropfindCalendarObject(w, r, owner, cal, path.Base(r.URL.Path))
			return
		}
	}

	// Root DAV path
	ms := common.MultiStatus{
		Resp: []common.Response{
			{
				Href: r.URL.Path,
				Props: []common.PropStat{{
					Prop: common.Prop{
						ResourceType:           common.MakeCollectionResourcetype(),
						CurrentUserPrincipal:   &common.Href{Value: common.CurrentUserPrincipalHref(r.Context(), h.basePath)},
						PrincipalURL:           &common.Href{Value: common.CurrentUserPrincipalHref(r.Context(), h.basePath)},
						PrincipalCollectionSet: &common.Hrefs{Values: []string{common.JoinURL(h.basePath, "principals") + "/"}},
					},
					Status: common.Ok(),
				}},
			},
		},
	}
	common.WriteMultiStatus(w, ms)
}

func (h *Handlers) propfindPrincipal(w http.ResponseWriter, r *http.Request, depth string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	self := common.PrincipalURL(h.basePath, u.UID)
	ms := common.MultiStatus{
		Resp: []common.Response{
			{
				Href: self,
				Props: []common.PropStat{{
					Prop: common.Prop{
						ResourceType:         common.MakePrincipalResourcetype(),
						DisplayName:          &u.DisplayName,
						PrincipalURL:         &common.Href{Value: self},
						CurrentUserPrincipal: &common.Href{Value: self},
						CalendarHomeSet:      &common.Href{Value: common.CalendarHome(h.basePath, u.UID)},
					},
					Status: common.Ok(),
				}},
			},
		},
	}
	common.WriteMultiStatus(w, ms)
}
