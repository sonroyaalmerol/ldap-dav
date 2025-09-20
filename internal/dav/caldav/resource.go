package caldav

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

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
	home := calendarHome(c.basePath, owner)

	// Only allow user to view own home listing
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
		for _, cc := range owned {
			hrefStr := calendarPath(c.basePath, owner, cc.URI)
			resps = append(resps, common.Response{
				Href: hrefStr,
				Props: []common.PropStat{{Prop: common.Prop{
					ResourceType:                  common.MakeCalendarResourcetype(),
					DisplayName:                   &cc.DisplayName,
					Owner:                         &common.Href{Value: common.PrincipalURL(c.basePath, owner)},
					SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
					GetCTag:                       &cc.CTag,
					SyncToken:                     &cc.CTag,
					ACL:                           common.BuildReadOnlyACL(r, c.basePath, cc.URI, owner, c.handlers.aclProv),
				}, Status: common.Ok()},
				}})
		}

		// Shared calendars container
		sharedBase := sharedRoot(c.basePath, owner)
		resps = append(resps, common.Response{
			Href: sharedBase,
			Props: []common.PropStat{{Prop: common.Prop{
				ResourceType: common.MakeCollectionResourcetype(),
				DisplayName:  common.StrPtr("Shared"),
			}, Status: common.Ok()},
			}})
		all, err := c.handlers.store.ListAllCalendars(r.Context())
		if err == nil {
			for _, cc := range all {
				if cc.OwnerUserID == owner {
					continue
				}
				if eff, aok := visible[cc.URI]; aok && eff.CanRead() {
					hrefStr := common.JoinURL(sharedBase, cc.URI) + "/"
					resps = append(resps, common.Response{
						Href: hrefStr,
						Props: []common.PropStat{{Prop: common.Prop{
							ResourceType:                  common.MakeCalendarResourcetype(),
							DisplayName:                   &cc.DisplayName,
							Owner:                         &common.Href{Value: c.ownerPrincipalForCalendar(cc)},
							SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
							GetCTag:                       &cc.CTag,
							SyncToken:                     &cc.CTag,
							ACL:                           common.BuildReadOnlyACL(r, c.basePath, cc.URI, owner, c.handlers.aclProv),
						}, Status: common.Ok()},
						}})
				}
			}
		}
	}

	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (c *CalDAVResourceHandler) PropfindCollection(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	// Resolve calendar by owner+uri
	cals, err := c.handlers.store.ListCalendarsByOwnerUser(r.Context(), owner)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	var cal *storage.Calendar
	for _, c := range cals {
		if c.URI == collection {
			cal = c
			break
		}
	}
	// Shared container itself
	if cal == nil && collection == "shared" {
		resps := []common.Response{{
			Href: common.JoinURL(c.basePath, "calendars", owner, "shared") + "/",
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

	prop := common.Prop{
		ResourceType:                  common.MakeCalendarResourcetype(),
		DisplayName:                   &cal.DisplayName,
		Owner:                         &common.Href{Value: common.PrincipalURL(c.basePath, owner)},
		SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
		GetCTag:                       &cal.CTag,
		SyncToken:                     &cal.CTag,
		GetLastModified:               cal.UpdatedAt.UTC().Format(time.RFC1123),
		ACL:                           common.BuildReadOnlyACL(r, c.basePath, collection, owner, c.handlers.aclProv),

		// CalDAV-specific properties
		CalendarDescription:     common.StrPtr(cal.Description),
		CalendarTimezone:        c.getCalendarTimezone(cal),
		SupportedCalendarData:   &common.SupportedCalData{ContentType: "text/calendar", Version: "2.0"},
		MaxResourceSize:         common.IntPtr(c.getMaxResourceSize()),
		MinDateTime:             common.StrPtr("19000101T000000Z"),
		MaxDateTime:             common.StrPtr("20380119T031407Z"),
		MaxInstances:            common.IntPtr(1000),
		MaxAttendeesPerInstance: common.IntPtr(100),
		SupportedCollationSet:   c.getSupportedCollationSet(),

		// Quota properties
		QuotaAvailableBytes: c.getQuotaAvailableBytes(cal),
		QuotaUsedBytes:      c.getQuotaUsedBytes(cal),

		// Supported reports
		SupportedReportSet: c.getSupportedReportSet(),
	}

	// Depth 0: collection props
	resps := []common.Response{{
		Href:  calendarPath(c.basePath, owner, collection),
		Props: []common.PropStat{{Prop: prop, Status: common.Ok()}},
	}}

	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (c *CalDAVResourceHandler) PropfindObject(w http.ResponseWriter, r *http.Request, owner, collection, object string) {
	uid := strings.TrimSuffix(object, filepath.Ext(object))
	calendarID, calOwner, err := c.handlers.resolveCalendar(r.Context(), owner, collection)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// ACL check read
	pr := common.MustPrincipal(r.Context())
	okRead, err := c.handlers.aclCheckRead(r.Context(), pr, collection, calOwner)
	if err != nil || !okRead {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, err = c.handlers.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hrefStr := common.JoinURL(c.handlers.basePath, "calendars", owner, collection, uid+".ics")
	resps := []common.Response{{
		Href: hrefStr,
		Props: []common.PropStat{{Prop: common.Prop{
			ContentType: common.CalContentType(),
			GetLastModified: func() string {
				if obj, err := c.handlers.store.GetObject(r.Context(), calendarID, uid); err == nil && !obj.UpdatedAt.IsZero() {
					return obj.UpdatedAt.UTC().Format(time.RFC1123)
				}
				return ""
			}(),
		}, Status: common.Ok()}},
	}}
	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (c *CalDAVResourceHandler) GetHomeSetProperty(basePath, uid string) interface{} {
	return &common.Href{Value: calendarHome(basePath, uid)}
}

func (c *CalDAVResourceHandler) ownerPrincipalForCalendar(cal *storage.Calendar) string {
	if cal.OwnerUserID != "" {
		return common.PrincipalURL(c.basePath, cal.OwnerUserID)
	}
	// could be group-owned; expose group principal path if implemented
	return common.JoinURL(c.basePath, "principals")
}

func (c *CalDAVResourceHandler) getCalendarTimezone(cal *storage.Calendar) *string {
	// TODO: implement proper with storage
	// Default to UTC
	return common.StrPtr("BEGIN:VTIMEZONE\r\nTZID:UTC\r\nEND:VTIMEZONE\r\n")
}

func (c *CalDAVResourceHandler) getMaxResourceSize() int {
	// Default 10MB limit
	return 10 * 1024 * 1024
}

func (c *CalDAVResourceHandler) getSupportedCollationSet() *common.SupportedCollationSet {
	return &common.SupportedCollationSet{
		SupportedCollation: []common.SupportedCollation{
			{Value: "i;ascii-casemap"},
			{Value: "i;octet"},
			{Value: "i;unicode-casemap"},
		},
	}
}

func (c *CalDAVResourceHandler) getSupportedReportSet() *common.SupportedReportSet {
	return &common.SupportedReportSet{
		SupportedReport: []common.SupportedReport{
			{Report: common.ReportType{CalendarQuery: &struct{}{}}},
			{Report: common.ReportType{CalendarMultiget: &struct{}{}}},
			{Report: common.ReportType{FreeBusyQuery: &struct{}{}}},
			{Report: common.ReportType{SyncCollection: &struct{}{}}},
		},
	}
}

func (c *CalDAVResourceHandler) getQuotaAvailableBytes(cal *storage.Calendar) *int64 {
	// TODO: implement proper with storage
	available := int64(1024 * 1024 * 1024) // 1GB default
	return &available
}

func (c *CalDAVResourceHandler) getQuotaUsedBytes(cal *storage.Calendar) *int64 {
	// TODO: implement proper with storage
	used := int64(0)
	// You might query your storage backend here to get actual usage
	return &used
}
