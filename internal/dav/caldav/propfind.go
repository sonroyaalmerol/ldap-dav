package caldav

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (h *Handlers) PropfindCalendarObject(w http.ResponseWriter, r *http.Request, ownerUID, calURI, filename string) {
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), ownerUID, calURI)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// ACL check read
	pr := common.MustPrincipal(r.Context())
	okRead, err := h.aclCheckRead(r.Context(), pr, calURI, calOwner)
	if err != nil || !okRead {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, err = h.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hrefStr := common.JoinURL(h.basePath, "calendars", ownerUID, calURI, uid+".ics")
	resps := []common.Response{{
		Href: hrefStr,
		Props: []common.PropStat{{Prop: common.Prop{
			ContentType: common.CalContentType(),
			GetLastModified: func() string {
				if obj, err := h.store.GetObject(r.Context(), calendarID, uid); err == nil && !obj.UpdatedAt.IsZero() {
					return obj.UpdatedAt.UTC().Format(time.RFC1123)
				}
				return ""
			}(),
		}, Status: common.Ok()}},
	}}
	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (h *Handlers) PropfindCalendarHome(w http.ResponseWriter, r *http.Request, ownerUID, depth string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	home := common.CalendarHome(h.basePath, ownerUID)

	// Only allow user to view own home listing
	if u.UID != ownerUID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	owned, err := h.store.ListCalendarsByOwnerUser(r.Context(), ownerUID)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	visible, err := h.aclProv.VisibleCalendars(r.Context(), u)
	if err != nil {
		http.Error(w, "acl error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response
	// Home itself
	resps = append(resps, common.Response{
		Href: home,
		Props: []common.PropStat{{Prop: common.Prop{
			ResourceType: common.MakeCollectionResourcetype(),
			DisplayName:  common.StrPtr("Calendar Home"),
		}, Status: common.Ok()}},
	})

	if depth == "1" {
		// Owned calendars
		for _, c := range owned {
			hrefStr := common.CalendarPath(h.basePath, ownerUID, c.URI)
			resps = append(resps, common.Response{
				Href: hrefStr,
				Props: []common.PropStat{{Prop: common.Prop{
					ResourceType:                  common.MakeCalendarResourcetype(),
					DisplayName:                   &c.DisplayName,
					Owner:                         &common.Href{Value: common.PrincipalURL(h.basePath, ownerUID)},
					SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
					GetCTag:                       &c.CTag,
					SyncToken:                     &c.CTag,
					ACL:                           common.BuildReadOnlyACL(r, h.basePath, c.URI, ownerUID, h.aclProv),
				}, Status: common.Ok()},
				}})
		}

		// Shared calendars container
		sharedBase := common.SharedRoot(h.basePath, ownerUID)
		resps = append(resps, common.Response{
			Href: sharedBase,
			Props: []common.PropStat{{Prop: common.Prop{
				ResourceType: common.MakeCollectionResourcetype(),
				DisplayName:  common.StrPtr("Shared"),
			}, Status: common.Ok()},
			}})
		all, err := h.store.ListAllCalendars(r.Context())
		if err == nil {
			for _, c := range all {
				if c.OwnerUserID == ownerUID {
					continue
				}
				if eff, aok := visible[c.URI]; aok && eff.CanRead() {
					hrefStr := common.JoinURL(sharedBase, c.URI) + "/"
					resps = append(resps, common.Response{
						Href: hrefStr,
						Props: []common.PropStat{{Prop: common.Prop{
							ResourceType:                  common.MakeCalendarResourcetype(),
							DisplayName:                   &c.DisplayName,
							Owner:                         &common.Href{Value: h.ownerPrincipalForCalendar(c)},
							SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
							GetCTag:                       &c.CTag,
							SyncToken:                     &c.CTag,
							ACL:                           common.BuildReadOnlyACL(r, h.basePath, c.URI, ownerUID, h.aclProv),
						}, Status: common.Ok()},
						}})
				}
			}
		}
	}

	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (h *Handlers) PropfindCalendarCollection(w http.ResponseWriter, r *http.Request, ownerUID, calURI, depth string) {
	// Resolve calendar by owner+uri
	cals, err := h.store.ListCalendarsByOwnerUser(r.Context(), ownerUID)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	var cal *storage.Calendar
	for _, c := range cals {
		if c.URI == calURI {
			cal = c
			break
		}
	}
	// Shared container itself
	if cal == nil && calURI == "shared" {
		resps := []common.Response{{
			Href: common.JoinURL(h.basePath, "calendars", ownerUID, "shared") + "/",
			Props: []common.PropStat{{Prop: common.Prop{
				ResourceType: common.MakeCollectionResourcetype(),
				DisplayName:  common.StrPtr("Shared"),
			}, Status: common.Ok()}},
		}}
		common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
		return
	}

	if cal == nil {
		http.NotFound(w, r)
		return
	}

	// Depth 0: collection props
	resps := []common.Response{{
		Href: common.CalendarPath(h.basePath, ownerUID, calURI),
		Props: []common.PropStat{{Prop: common.Prop{
			ResourceType:                  common.MakeCalendarResourcetype(),
			DisplayName:                   &cal.DisplayName,
			Owner:                         &common.Href{Value: common.PrincipalURL(h.basePath, ownerUID)},
			SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
			GetCTag:                       &cal.CTag,
			SyncToken:                     &cal.CTag,
			GetLastModified:               cal.UpdatedAt.UTC().Format(time.RFC1123),
			ACL:                           common.BuildReadOnlyACL(r, h.basePath, calURI, ownerUID, h.aclProv),
		}, Status: common.Ok()}},
	}}

	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (h *Handlers) ownerPrincipalForCalendar(c *storage.Calendar) string {
	if c.OwnerUserID != "" {
		return common.PrincipalURL(h.basePath, c.OwnerUserID)
	}
	// could be group-owned; expose group principal path if implemented
	return common.JoinURL(h.basePath, "principals")
}

