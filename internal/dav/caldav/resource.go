package caldav

import (
	"encoding/xml"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

type CalDAVResourceHandler struct {
	handlers *Handlers
	basePath string
}

func NewCalDAVResourceHandler(handlers *Handlers, basePath string) *CalDAVResourceHandler {
	return &CalDAVResourceHandler{
		handlers: handlers,
		basePath: basePath,
	}
}

func (c *CalDAVResourceHandler) SplitResourcePath(urlPath string) (owner, collection string, rest []string) {
	return splitResourcePath(urlPath, c.basePath)
}

func (c *CalDAVResourceHandler) PropfindHome(w http.ResponseWriter, r *http.Request, owner, depth string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		c.handlers.logger.Error().Str("path", r.URL.Path).Msg("PROPFIND home unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	home := common.CalendarHome(c.basePath, owner)

	if u.UID != owner {
		c.handlers.logger.Debug().Str("user", u.UID).Str("owner", owner).Msg("PROPFIND home forbidden - user mismatch")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	c.handlers.ensurePersonalCalendar(r.Context(), owner)

	owned, err := c.handlers.store.ListCalendarsByOwnerUser(r.Context(), owner)
	if err != nil {
		c.handlers.logger.Error().Err(err).Str("owner", owner).Msg("failed to list owned calendars in PROPFIND home")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	visible, err := c.handlers.aclProv.VisibleCalendars(r.Context(), u)
	if err != nil {
		c.handlers.logger.Error().Err(err).Str("user", u.UID).Msg("failed to compute visible calendars in PROPFIND home")
		http.Error(w, "acl error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	homeResp := common.Response{Hrefs: []common.Href{{Value: home}}}
	_ = homeResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
	_ = homeResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Calendar Home"})
	_ = homeResp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
	_ = homeResp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})

	_ = homeResp.EncodeProp(http.StatusOK, c.buildSupportedPrivilegeSet())
	_ = homeResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
		Privilege: []common.Privilege{{All: &struct{}{}}},
	})
	_ = homeResp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))

	resps = append(resps, homeResp)

	if depth == "1" {
		for _, cc := range owned {
			hrefStr := common.CalendarPath(c.basePath, owner, cc.URI)
			resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
			_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Calendar: &struct{}{}})
			_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: cc.DisplayName})
			_ = resp.EncodeProp(http.StatusOK, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
				Text    string   `xml:",chardata"`
			}{Text: cc.Color})
			_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
			_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
			_ = resp.EncodeProp(http.StatusOK, common.SupportedCompSet{
				Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}},
			})
			_ = resp.EncodeProp(http.StatusOK, supportedReportSetValue())
			_ = resp.EncodeProp(http.StatusOK, struct {
				XMLName xml.Name `xml:"DAV: sync-token"`
				Text    string   `xml:",chardata"`
			}{Text: cc.CTag})
			_ = resp.EncodeProp(http.StatusOK, struct {
				XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
				Text    string   `xml:",chardata"`
			}{Text: cc.CTag})

			_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
				Privilege: []common.Privilege{{All: &struct{}{}}},
			})

			_ = resp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))
			resps = append(resps, resp)
		}

		sharedBase := common.CalendarSharedRoot(c.basePath, owner)
		sharedResp := common.Response{Hrefs: []common.Href{{Value: sharedBase}}}
		_ = sharedResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
		_ = sharedResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Shared"})
		_ = sharedResp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})

		sharedEffectivePrivileges := acl.Effective{
			Read:                        true,
			ReadCurrentUserPrivilegeSet: true,
		}

		if sharedEffectivePrivileges.CanReadCurrentUserPrivilegeSet() {
			_ = sharedResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
				Privilege: []common.Privilege{{Read: &struct{}{}}},
			})
		}

		resps = append(resps, sharedResp)

		all, err := c.handlers.store.ListAllCalendars(r.Context())
		if err != nil {
			c.handlers.logger.Error().Err(err).Msg("failed to list all calendars in PROPFIND home")
		} else {
			for _, cc := range all {
				if cc.OwnerUserID == owner {
					continue
				}
				if eff, aok := visible[cc.URI]; aok && eff.CanRead() {
					hrefStr := common.JoinURL(sharedBase, cc.URI) + "/"
					resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
					_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Calendar: &struct{}{}})
					_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: cc.DisplayName})
					_ = resp.EncodeProp(http.StatusOK, struct {
						XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
						Text    string   `xml:",chardata"`
					}{Text: cc.Color})
					_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: c.ownerPrincipalForCalendar(cc)}})
					_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
					_ = resp.EncodeProp(http.StatusOK, common.SupportedCompSet{
						Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}},
					})
					_ = resp.EncodeProp(http.StatusOK, supportedReportSetValue())
					_ = resp.EncodeProp(http.StatusOK, struct {
						XMLName xml.Name `xml:"DAV: sync-token"`
						Text    string   `xml:",chardata"`
					}{Text: cc.CTag})
					_ = resp.EncodeProp(http.StatusOK, struct {
						XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
						Text    string   `xml:",chardata"`
					}{Text: cc.CTag})

					if eff.CanReadCurrentUserPrivilegeSet() {
						privs := c.effectiveToPrivileges(eff)
						_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{Privilege: privs})
					}

					if eff.CanReadACL() {
						acl := c.buildSharedACL(cc.OwnerUserID, owner, eff)
						_ = resp.EncodeProp(http.StatusOK, acl)
					}
					resps = append(resps, resp)
				}
			}
		}
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus in PROPFIND home")
	}
}

func (c *CalDAVResourceHandler) PropfindCollection(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	if strings.HasSuffix(collection, "-inbox") {
		c.propfindSchedulingInbox(w, r, owner, collection, depth)
		return
	}
	if strings.HasSuffix(collection, "-outbox") {
		c.propfindSchedulingOutbox(w, r, owner, collection, depth)
		return
	}

	requesterUID := owner

	cals, err := c.handlers.store.ListCalendarsByOwnerUser(r.Context(), owner)
	if err != nil {
		c.handlers.logger.Error().Err(err).Str("owner", owner).Msg("failed to list calendars by owner in PROPFIND collection")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	var cal *storage.Calendar
	for _, cc := range cals {
		if cc.URI == collection {
			cal = cc
			break
		}
	}

	var trueOwner string
	isSharedMount := false
	if cal == nil && collection != "shared" {
		if sc, err := c.handlers.store.GetCalendarByURI(r.Context(), collection); err == nil && sc != nil {
			pr := common.MustPrincipal(r.Context())
			if ok, err := c.handlers.aclCheckRead(r.Context(), pr, sc.URI, sc.OwnerUserID); err == nil && ok {
				cal = sc
				trueOwner = sc.OwnerUserID
				isSharedMount = true
			} else if err != nil {
				c.handlers.logger.Error().Err(err).
					Str("calendar", sc.URI).
					Str("owner", sc.OwnerUserID).
					Msg("ACL check failed in PROPFIND collection reading shared calendar")
			}
		}
	}

	if cal == nil && collection == "shared" {
		resp := common.Response{
			Hrefs: []common.Href{{Value: common.JoinURL(c.basePath, "calendars", owner, "shared") + "/"}},
		}
		_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
		_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Shared"})

		pr := common.MustPrincipal(r.Context())
		_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, pr.UserID)}})

		_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
			Privilege: []common.Privilege{{Read: &struct{}{}}},
		})

		ms := common.MultiStatus{Responses: []common.Response{resp}}
		if err := common.ServeMultiStatus(w, &ms); err != nil {
			c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND shared collection")
		}
		return
	}

	if cal == nil {
		c.handlers.logger.Debug().Str("owner", owner).Str("collection", collection).Msg("collection not found in PROPFIND")
		http.NotFound(w, r)
		return
	}

	var href string
	var ownerHref string
	if isSharedMount {
		href = common.JoinURL(common.CalendarSharedRoot(c.basePath, requesterUID), collection) + "/"
		ownerHref = common.PrincipalURL(c.basePath, trueOwner)
	} else {
		href = common.CalendarPath(c.basePath, owner, collection)
		ownerHref = common.PrincipalURL(c.basePath, owner)
	}

	pr := common.MustPrincipal(r.Context())

	propResp := common.Response{
		Hrefs: []common.Href{{Value: href}},
	}

	_ = propResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Calendar: &struct{}{}})
	_ = propResp.EncodeProp(http.StatusOK, common.DisplayName{Name: cal.DisplayName})
	_ = propResp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: ownerHref}})
	_ = propResp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, pr.UserID)}})

	_ = propResp.EncodeProp(http.StatusOK, common.SupportedCompSet{
		Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}},
	})
	_ = propResp.EncodeProp(http.StatusOK, supportedReportSetValue())
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"DAV: sync-token"`
		Text    string   `xml:",chardata"`
	}{Text: cal.CTag})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
		Text    string   `xml:",chardata"`
	}{Text: cal.CTag})

	if !cal.UpdatedAt.IsZero() {
		_ = propResp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(cal.UpdatedAt.UTC())})
	}

	_ = propResp.EncodeProp(http.StatusOK, c.buildSupportedPrivilegeSet())

	if isSharedMount && trueOwner != "" && pr.UserID != trueOwner {
		if eff, err := c.handlers.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, collection); err == nil {
			if eff.CanReadCurrentUserPrivilegeSet() {
				currentUserPrivs := c.effectiveToPrivileges(eff)
				_ = propResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{Privilege: currentUserPrivs})
			}

			if eff.CanReadACL() {
				acl := c.buildCollectionACL(trueOwner, pr.UserID, isSharedMount, eff)
				_ = propResp.EncodeProp(http.StatusOK, acl)
			}
		}
	} else {
		acl := c.buildOwnerACL(pr.UserID)
		_ = propResp.EncodeProp(http.StatusOK, acl)
	}

	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
		Text    string   `xml:",chardata"`
	}{Text: cal.Description})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
		Text    string   `xml:",chardata"`
	}{Text: cal.Color})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-timezone"`
		Text    string   `xml:",chardata"`
	}{Text: c.getCalendarTimezone(cal)})

	_ = propResp.EncodeProp(http.StatusOK, common.SupportedCalData{ContentType: "text/calendar", Version: "2.0"})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav max-resource-size"`
		Size    int      `xml:",chardata"`
	}{Size: c.getMaxResourceSize()})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav min-date-time"`
		Text    string   `xml:",chardata"`
	}{Text: "19000101T000000Z"})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav max-date-time"`
		Text    string   `xml:",chardata"`
	}{Text: "20380119T031407Z"})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav max-instances"`
		N       int      `xml:",chardata"`
	}{N: 1000})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav max-attendees-per-instance"`
		N       int      `xml:",chardata"`
	}{N: 100})

	_ = propResp.EncodeProp(http.StatusOK, c.getSupportedCollationSetValue())

	ms := common.MultiStatus{Responses: []common.Response{propResp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND collection")
	}
}

func (c *CalDAVResourceHandler) PropfindObject(w http.ResponseWriter, r *http.Request, owner, collection, object string) {
	uid := strings.TrimSuffix(object, filepath.Ext(object))
	calendarID, calOwner, err := c.handlers.resolveCalendar(r.Context(), owner, collection)
	if err != nil {
		c.handlers.logger.Error().Err(err).
			Str("owner", owner).
			Str("collection", collection).
			Str("object", object).
			Msg("failed to resolve calendar in PROPFIND object")
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	pr := common.MustPrincipal(r.Context())
	okRead, err := c.handlers.aclCheckRead(r.Context(), pr, collection, calOwner)
	if err != nil || !okRead {
		c.handlers.logger.Debug().Err(err).
			Bool("can_read", okRead).
			Str("user", pr.UserID).
			Str("collection", collection).
			Msg("ACL check failed or denied in PROPFIND object")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	obj, err := c.handlers.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		c.handlers.logger.Debug().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("object not found in PROPFIND object")
		http.NotFound(w, r)
		return
	}
	hrefStr := common.JoinURL(c.handlers.basePath, "calendars", owner, collection, uid+".ics")

	resp := common.Response{
		Hrefs: []common.Href{{Value: hrefStr}},
	}
	_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/calendar; charset=utf-8"})
	if !obj.UpdatedAt.IsZero() {
		_ = resp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(obj.UpdatedAt.UTC())})
	}

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND object")
	}
}

func (c *CalDAVResourceHandler) GetHomeSetProperty(basePath, uid string) interface{} {
	return &common.Href{Value: common.CalendarHome(basePath, uid)}
}

func (c *CalDAVResourceHandler) ownerPrincipalForCalendar(cal *storage.Calendar) string {
	if cal.OwnerUserID != "" {
		return common.PrincipalURL(c.basePath, cal.OwnerUserID)
	}
	return common.JoinURL(c.basePath, "principals")
}

func (c *CalDAVResourceHandler) getCalendarTimezone(_ *storage.Calendar) string {
	return "BEGIN:VTIMEZONE\r\nTZID:UTC\r\nEND:VTIMEZONE\r\n"
}

func (c *CalDAVResourceHandler) getMaxResourceSize() int {
	return 10 * 1024 * 1024
}

func (c *CalDAVResourceHandler) getSupportedCollationSetValue() interface{} {
	return &common.SupportedCollationSet{
		SupportedCollation: []common.SupportedCollation{
			{Value: "i;ascii-casemap"},
			{Value: "i;octet"},
			{Value: "i;unicode-casemap"},
		},
	}
}

func supportedReportSetValue() interface{} {
	return &common.SupportedReportSet{
		SupportedReport: []common.SupportedReport{
			{Report: common.ReportType{CalendarQuery: &struct{}{}}},
			{Report: common.ReportType{CalendarMultiget: &struct{}{}}},
			{Report: common.ReportType{SyncCollection: &struct{}{}}},
		},
	}
}
