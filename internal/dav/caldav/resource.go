package caldav

import (
	"encoding/xml"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
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
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	home := common.CalendarHome(c.basePath, owner)

	if u.UID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	owned, err := c.handlers.store.ListCalendarsByOwnerUser(r.Context(), owner)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	visible, err := c.handlers.aclProv.VisibleCalendars(r.Context(), u)
	if err != nil {
		http.Error(w, "acl error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	homeResp := common.Response{Hrefs: []common.Href{{Value: home}}}
	_ = homeResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
	_ = homeResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Calendar Home"})
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
			_ = resp.EncodeProp(http.StatusOK, struct {
				XMLName xml.Name `xml:"DAV: owner"`
				Href    common.Href
			}{Href: common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
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
			if acl := common.BuildReadOnlyACL(r, c.basePath, cc.URI, owner, c.handlers.aclProv); acl != nil {
				_ = resp.EncodeProp(http.StatusOK, *acl)
			}
			resps = append(resps, resp)
		}

		sharedBase := common.CalendarSharedRoot(c.basePath, owner)
		sharedResp := common.Response{Hrefs: []common.Href{{Value: sharedBase}}}
		_ = sharedResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
		_ = sharedResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Shared"})
		resps = append(resps, sharedResp)

		all, err := c.handlers.store.ListAllCalendars(r.Context())
		if err == nil {
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
					_ = resp.EncodeProp(http.StatusOK, struct {
						XMLName xml.Name `xml:"DAV: owner"`
						Href    common.Href
					}{Href: common.Href{Value: c.ownerPrincipalForCalendar(cc)}})
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
					if acl := common.BuildReadOnlyACL(r, c.basePath, cc.URI, owner, c.handlers.aclProv); acl != nil {
						_ = resp.EncodeProp(http.StatusOK, *acl)
					}
					resps = append(resps, resp)
				}
			}
		}
	}

	ms := common.MultiStatus{Responses: resps}
	_ = common.ServeMultiStatus(w, &ms)
}

func (c *CalDAVResourceHandler) PropfindCollection(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	requesterUID := owner

	cals, err := c.handlers.store.ListCalendarsByOwnerUser(r.Context(), owner)
	if err != nil {
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
		if sc, err := c.handlers.findCalendarByURI(r.Context(), collection); err == nil && sc != nil {
			pr := common.MustPrincipal(r.Context())
			if ok, err := c.handlers.aclCheckRead(r.Context(), pr, sc.URI, sc.OwnerUserID); err == nil && ok {
				cal = sc
				trueOwner = sc.OwnerUserID
				isSharedMount = true
			}
		}
	}

	if cal == nil && collection == "shared" {
		resp := common.Response{
			Hrefs: []common.Href{{Value: common.JoinURL(c.basePath, "calendars", owner, "shared") + "/"}},
		}
		_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
		_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Shared"})
		ms := common.MultiStatus{Responses: []common.Response{resp}}
		_ = common.ServeMultiStatus(w, &ms)
		return
	}

	if cal == nil {
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

	propResp := common.Response{
		Hrefs: []common.Href{{Value: href}},
	}
	_ = propResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Calendar: &struct{}{}})
	_ = propResp.EncodeProp(http.StatusOK, common.DisplayName{Name: cal.DisplayName})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"DAV: owner"`
		Href    common.Href
	}{Href: common.Href{Value: ownerHref}})
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

	if acl := common.BuildReadOnlyACL(r, c.basePath, collection, owner, c.handlers.aclProv); acl != nil {
		_ = propResp.EncodeProp(http.StatusOK, *acl)
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
	_ = common.ServeMultiStatus(w, &ms)
}

func (c *CalDAVResourceHandler) PropfindObject(w http.ResponseWriter, r *http.Request, owner, collection, object string) {
	uid := strings.TrimSuffix(object, filepath.Ext(object))
	calendarID, calOwner, err := c.handlers.resolveCalendar(r.Context(), owner, collection)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	pr := common.MustPrincipal(r.Context())
	okRead, err := c.handlers.aclCheckRead(r.Context(), pr, collection, calOwner)
	if err != nil || !okRead {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	obj, err := c.handlers.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
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
	_ = common.ServeMultiStatus(w, &ms)
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
