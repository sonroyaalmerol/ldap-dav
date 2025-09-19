package dav

import (
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (h *Handlers) HandlePropfind(w http.ResponseWriter, r *http.Request) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "0"
	}

	_, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()

	// Identify resource kind based on path
	if isPrincipalUsers(r.URL.Path, h.basePath) {
		h.propfindPrincipal(w, r, depth)
		return
	}
	if owner, cal, rest := h.splitCalendarPath(r.URL.Path); owner != "" {
		if len(rest) == 0 {
			// calendar collection or home
			if cal == "" {
				// home set listing calendars
				h.propfindCalendarHome(w, r, owner, depth)
				return
			}
			// calendar collection
			h.propfindCalendarCollection(w, r, owner, cal, depth)
			return
		}
		// object PROPFIND
		h.propfindCalendarObject(w, r, owner, cal, path.Base(r.URL.Path))
		return
	}

	// Root DAV path
	ms := multistatus{
		Resp: []response{
			{
				Href: r.URL.Path,
				Prop: propstat{
					Prop: prop{
						Resourcetype:           makeCollectionResourcetype(),
						CurrentUserPrincipal:   &href{Value: h.currentUserPrincipalHref(r.Context())},
						PrincipalURL:           &href{Value: h.currentUserPrincipalHref(r.Context())},
						PrincipalCollectionSet: &hrefs{Values: []string{joinURL(h.basePath, "principals") + "/"}},
					},
					Status: ok(),
				},
			},
		},
	}
	writeMultiStatus(w, ms)
}

func (h *Handlers) propfindPrincipal(w http.ResponseWriter, r *http.Request, depth string) {
	u, _ := h.currentUser(r.Context())
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	self := h.principalURL(u.UID)
	ms := multistatus{
		Resp: []response{
			{
				Href: self,
				Prop: propstat{
					Prop: prop{
						Resourcetype:         makePrincipalResourcetype(),
						DisplayName:          &u.DisplayName,
						PrincipalURL:         &href{Value: self},
						CurrentUserPrincipal: &href{Value: self},
						CalendarHomeSet:      &href{Value: h.calendarHome(u.UID)},
					},
					Status: ok(),
				},
			},
		},
	}
	writeMultiStatus(w, ms)
}

func (h *Handlers) propfindCalendarHome(w http.ResponseWriter, r *http.Request, ownerUID, depth string) {
	u, _ := h.currentUser(r.Context())
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	home := h.calendarHome(ownerUID)

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

	var resps []response
	// Home itself
	resps = append(resps, response{
		Href: home,
		Prop: propstat{Prop: prop{
			Resourcetype: makeCollectionResourcetype(),
			DisplayName:  strPtr("Calendar Home"),
		}, Status: ok()},
	})

	if depth == "1" {
		// Owned calendars
		for _, c := range owned {
			hrefStr := h.calendarPath(ownerUID, c.URI)
			resps = append(resps, response{
				Href: hrefStr,
				Prop: propstat{Prop: prop{
					Resourcetype:                  makeCalendarResourcetype(),
					DisplayName:                   &c.DisplayName,
					Owner:                         &href{Value: h.principalURL(ownerUID)},
					SupportedCalendarComponentSet: &supportedCompSet{Comp: []comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
					GetCTag:                       &c.CTag,
					SyncToken:                     &c.CTag,
					ACL:                           h.buildReadOnlyACL(r, c.URI, ownerUID),
				}, Status: ok()},
			})
		}

		// Shared calendars container
		sharedBase := h.sharedRoot(ownerUID)
		resps = append(resps, response{
			Href: sharedBase,
			Prop: propstat{Prop: prop{
				Resourcetype: makeCollectionResourcetype(),
				DisplayName:  strPtr("Shared"),
			}, Status: ok()},
		})
		all, err := h.store.ListAllCalendars(r.Context())
		if err == nil {
			for _, c := range all {
				if c.OwnerUserID == ownerUID {
					continue
				}
				if eff, aok := visible[c.URI]; aok && eff.CanRead() {
					hrefStr := joinURL(sharedBase, c.URI) + "/"
					resps = append(resps, response{
						Href: hrefStr,
						Prop: propstat{Prop: prop{
							Resourcetype:                  makeCalendarResourcetype(),
							DisplayName:                   &c.DisplayName,
							Owner:                         &href{Value: h.ownerPrincipalForCalendar(c)},
							SupportedCalendarComponentSet: &supportedCompSet{Comp: []comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
							GetCTag:                       &c.CTag,
							SyncToken:                     &c.CTag,
							ACL:                           h.buildReadOnlyACL(r, c.URI, ownerUID),
						}, Status: ok()},
					})
				}
			}
		}
	}

	writeMultiStatus(w, multistatus{Resp: resps})
}

func (h *Handlers) propfindCalendarCollection(w http.ResponseWriter, r *http.Request, ownerUID, calURI, depth string) {
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
		resps := []response{{
			Href: joinURL(h.basePath, "calendars", ownerUID, "shared") + "/",
			Prop: propstat{Prop: prop{
				Resourcetype: makeCollectionResourcetype(),
				DisplayName:  strPtr("Shared"),
			}, Status: ok()},
		}}
		writeMultiStatus(w, multistatus{Resp: resps})
		return
	}

	if cal == nil {
		http.NotFound(w, r)
		return
	}

	// Depth 0: collection props
	resps := []response{{
		Href: h.calendarPath(ownerUID, calURI),
		Prop: propstat{Prop: prop{
			Resourcetype:                  makeCalendarResourcetype(),
			DisplayName:                   &cal.DisplayName,
			Owner:                         &href{Value: h.principalURL(ownerUID)},
			SupportedCalendarComponentSet: &supportedCompSet{Comp: []comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
			GetCTag:                       &cal.CTag,
			SyncToken:                     &cal.CTag,
			GetLastModified:               cal.UpdatedAt.UTC().Format(time.RFC1123),
			ACL:                           h.buildReadOnlyACL(r, calURI, ownerUID),
		}, Status: ok()},
	}}

	writeMultiStatus(w, multistatus{Resp: resps})
}

func (h *Handlers) propfindCalendarObject(w http.ResponseWriter, r *http.Request, ownerUID, calURI, filename string) {
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), ownerUID, calURI)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// ACL check read
	pr := mustPrincipal(r.Context())
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
	hrefStr := joinURL(h.basePath, "calendars", ownerUID, calURI, uid+".ics")
	resps := []response{{
		Href: hrefStr,
		Prop: propstat{Prop: prop{
			ContentType: calContentType(),
			GetLastModified: func() string {
				if obj, err := h.store.GetObject(r.Context(), calendarID, uid); err == nil && !obj.UpdatedAt.IsZero() {
					return obj.UpdatedAt.UTC().Format(time.RFC1123)
				}
				return ""
			}(),
		}, Status: ok()},
	}}
	writeMultiStatus(w, multistatus{Resp: resps})
}
