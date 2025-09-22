package caldav

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

func (h *Handlers) ReportCalendarQuery(w http.ResponseWriter, r *http.Request, q common.CalendarQuery) {
	owner, calURI, _ := splitResourcePath(r.URL.Path, h.basePath)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	props := common.ParsePropRequest(q.Prop)

	var start, end *time.Time
	if tr := common.ExtractTimeRange(q.Filter); tr != nil {
		if tr.Start != "" {
			if t, err := common.ParseICalTime(tr.Start); err == nil {
				start = &t
			}
		}
		if tr.End != "" {
			if t, err := common.ParseICalTime(tr.End); err == nil {
				end = &t
			}
		}
	}

	comps := common.ExtractComponentFilterNames(q.Filter)
	if len(comps) == 0 {
		comps = []string{"VEVENT", "VTODO", "VJOURNAL"}
	}

	objs, err := h.store.ListObjectsByComponent(r.Context(), calendarID, comps, start, end)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	if start != nil && end != nil && common.ContainsComponent(comps, "VEVENT") {
		resps = h.buildExpandedEventResponses(objs, *start, *end, props, owner, calURI)
	} else {
		// No time range or not querying events - return original objects
		for _, o := range objs {
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
		}
	}

	WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (h *Handlers) ReportCalendarMultiget(w http.ResponseWriter, r *http.Request, mg common.CalendarMultiget) {
	props := common.ParsePropRequest(mg.Prop)
	var resps []common.Response
	for _, hrefStr := range mg.Hrefs {
		owner, calURI, rest := splitResourcePath(hrefStr, h.basePath)
		if owner == "" || len(rest) == 0 {
			continue
		}
		filename := rest[len(rest)-1]
		uid := strings.TrimSuffix(filename, filepath.Ext(filename))

		uid = h.extractBaseUID(uid)

		calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
		if err != nil {
			continue
		}
		pr := common.MustPrincipal(r.Context())
		okRead, err := h.aclCheckRead(r.Context(), pr, calURI, calOwner)
		if err != nil || !okRead {
			continue
		}
		o, err := h.store.GetObject(r.Context(), calendarID, uid)
		if err != nil {
			continue
		}

		if h.isRecurringInstanceRequest(hrefStr) {
			instanceResp := h.handleRecurringInstanceRequest(hrefStr, o, props)
			if instanceResp != nil {
				resps = append(resps, *instanceResp)
			}
			continue
		}

		resps = append(resps, buildReportResponse(hrefStr, props, o))
	}
	WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func buildReportResponse(hrefStr string, props common.PropRequest, o *storage.Object) common.Response {
	p := common.Prop{}
	p.ContentType = common.CalContentType()
	if props.CalendarData {
		p.CalendarDataText = o.Data
	}
	if props.GetETag && o.ETag != "" {
		gt := `"` + o.ETag + `"`
		p.GetETag = gt
	}
	// Always include last-modified if available
	if !o.UpdatedAt.IsZero() {
		// RFC 1123 format required by DAV:getlastmodified
		p.GetLastModified = o.UpdatedAt.UTC().Format(time.RFC1123)
	}
	return common.Response{
		Href: hrefStr,
		Props: []common.PropStat{{
			Prop:   p,
			Status: common.Ok(),
		}},
	}
}

func (h *Handlers) ReportSyncCollection(w http.ResponseWriter, r *http.Request, sc common.SyncCollection) {
	owner, calURI, _ := splitResourcePath(r.URL.Path, h.basePath)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	curToken, _, err := h.store.GetSyncInfo(r.Context(), calendarID)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	sinceSeq := int64(0)
	if sc.SyncToken != "" {
		if ss, ok := common.ParseSeqToken(sc.SyncToken); ok {
			sinceSeq = ss
		}
	}
	limit := 0
	if sc.Limit != nil && sc.Limit.NResults > 0 {
		limit = sc.Limit.NResults
	}
	changes, _, err := h.store.ListChangesSince(r.Context(), calendarID, sinceSeq, limit)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	colHref := calendarPath(h.basePath, owner, calURI)
	resps := []common.Response{
		{
			Href: colHref,
			Props: []common.PropStat{{
				Prop: common.Prop{
					ResourceType:                  common.MakeCalendarResourcetype(),
					SupportedCalendarComponentSet: &common.SupportedCompSet{Comp: []common.Comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
					SyncToken:                     &curToken,
				},
				Status: common.Ok(),
			}},
		},
	}

	if limit > 0 && len(changes) == limit {
		n := len(changes)
		resps[0].Props = append(resps[0].Props, common.PropStat{
			Prop:   common.Prop{MatchesWithinLimits: &n},
			Status: common.Ok(),
		})
	}

	for _, ch := range changes {
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, ch.UID+".ics")
		if ch.Deleted {
			resps = append(resps, common.Response{
				Href: hrefStr,
				Props: []common.PropStat{{
					Prop:   common.Prop{},
					Status: "HTTP/1.1 404 Not Found",
				}},
			})
		} else {
			resps = append(resps, common.Response{
				Href: hrefStr,
				Props: []common.PropStat{{
					Prop:   common.Prop{ContentType: common.CalContentType()},
					Status: common.Ok(),
				}},
			})
		}
	}

	WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (h *Handlers) ReportFreeBusyQuery(w http.ResponseWriter, r *http.Request, fb common.FreeBusyQuery) {
	owner, calURI, _ := splitResourcePath(r.URL.Path, h.basePath)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	if fb.Time == nil || fb.Time.Start == "" || fb.Time.End == "" {
		http.Error(w, "time-range required", http.StatusBadRequest)
		return
	}
	start, err := common.ParseICalTime(fb.Time.Start)
	if err != nil {
		http.Error(w, "bad start", http.StatusBadRequest)
		return
	}
	end, err := common.ParseICalTime(fb.Time.End)
	if err != nil {
		http.Error(w, "bad end", http.StatusBadRequest)
		return
	}
	if !end.After(start) {
		http.Error(w, "end must be after start", http.StatusBadRequest)
		return
	}

	// Get VEVENTs and expand recurrences for free/busy calculation
	objs, err := h.store.ListObjectsByComponent(r.Context(), calendarID, []string{"VEVENT"}, &start, &end)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var busy []ical.Interval
	expander := ical.NewRecurrenceExpander(time.UTC)

	for _, o := range objs {
		if o.Component != "VEVENT" {
			continue
		}

		// Parse and expand recurring events
		events, err := ical.ParseCalendar([]byte(o.Data))
		if err != nil {
			// Fall back to using object's stored start/end times
			if o.StartAt != nil && o.EndAt != nil {
				if o.EndAt.After(start) && (end.After(*o.StartAt) || end.Equal(*o.StartAt)) {
					s := common.MaxTime(*o.StartAt, start)
					e := common.MinTime(*o.EndAt, end)
					if e.After(s) {
						busy = append(busy, ical.Interval{S: s, E: e})
					}
				}
			}
			continue
		}

		expandedEvents, err := expander.ExpandRecurrences(events, start, end)
		if err != nil {
			// Fall back to stored times
			if o.StartAt != nil && o.EndAt != nil {
				if o.EndAt.After(start) && (end.After(*o.StartAt) || end.Equal(*o.StartAt)) {
					s := common.MaxTime(*o.StartAt, start)
					e := common.MinTime(*o.EndAt, end)
					if e.After(s) {
						busy = append(busy, ical.Interval{S: s, E: e})
					}
				}
			}
			continue
		}

		// Add busy periods from all expanded instances
		for _, event := range expandedEvents {
			if event.End.After(start) && (end.After(event.Start) || end.Equal(event.Start)) {
				s := common.MaxTime(event.Start, start)
				e := common.MinTime(event.End, end)
				if e.After(s) {
					busy = append(busy, ical.Interval{S: s, E: e})
				}
			}
		}
	}

	busy = common.MergeIntervalsFB(busy)

	icsData := ical.BuildFreeBusyICS(start, end, busy, h.cfg.ICS.BuildProdID())

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Write(icsData)
}
