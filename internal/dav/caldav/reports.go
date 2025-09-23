package caldav

import (
	"encoding/xml"
	"fmt"
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
		for _, o := range objs {
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
		}
	}

	ms := common.MultiStatus{Responses: resps}
	_ = common.ServeMultiStatus(w, &ms)
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
	ms := common.MultiStatus{Responses: resps}
	_ = common.ServeMultiStatus(w, &ms)
}

func buildReportResponse(hrefStr string, props common.PropRequest, o *storage.Object) common.Response {
	resp := common.Response{
		Hrefs: []common.Href{{Value: hrefStr}},
	}
	_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/calendar; charset=utf-8"})
	if props.CalendarData {
		type CalendarData struct {
			XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
			Text    string   `xml:",chardata"`
		}
		_ = resp.EncodeProp(http.StatusOK, CalendarData{Text: o.Data})
	}
	if props.GetETag && o.ETag != "" {
		_ = resp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(o.ETag)})
	}
	if !o.UpdatedAt.IsZero() {
		_ = resp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(o.UpdatedAt)})
	}
	return resp
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

	props := common.ParsePropRequest(sc.Prop)

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

	var resps []common.Response

	for _, ch := range changes {
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, ch.UID+".ics")
		if ch.Deleted {
			resp := common.Response{
				Hrefs: []common.Href{{Value: hrefStr}},
			}
			resp.Status = &common.Status{Code: http.StatusNotFound}
			resps = append(resps, resp)
		} else {
			resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
			// Return only requested properties. For sync, clients typically request getetag.
			// Fetch object if any property requires it.
			var obj *storage.Object
			var getErr error
			needObject := props.CalendarData || props.GetETag
			if needObject {
				obj, getErr = h.store.GetObject(r.Context(), calendarID, ch.UID)
				if getErr != nil {
					// Object disappeared between change listing and fetch; treat like deleted.
					resp.Status = &common.Status{Code: http.StatusNotFound}
					resps = append(resps, resp)
					continue
				}
			}
			if props.GetETag && obj != nil && obj.ETag != "" {
				_ = resp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(obj.ETag)})
			}
			if props.CalendarData && obj != nil {
				type CalendarData struct {
					XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
					Text    string   `xml:",chardata"`
				}
				_ = resp.EncodeProp(http.StatusOK, CalendarData{Text: obj.Data})
			}
			resps = append(resps, resp)
		}
	}

	// RFC 6578: top-level sync-token and number-of-matches-within-limits
	ms := common.MultiStatus{
		Responses: resps,
		SyncToken: curToken,
	}
	if limit > 0 && len(changes) == limit {
		ms.NumberOfMatchesWithinLimits = fmt.Sprintf("%d", len(changes))
	}
	_ = common.ServeMultiStatus(w, &ms)
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

	// Validate and parse time range (required per RFC 4791)
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

	objs, err := h.store.ListObjectsByComponent(r.Context(), calendarID, []string{"VEVENT"}, &start, &end)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	// Process events and build busy intervals
	busy := h.buildBusyIntervals(objs, start, end)

	// Generate and send free/busy response
	icsData := common.BuildFreeBusyICS(start, end, busy, h.cfg.ICS.BuildProdID())
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = w.Write(icsData)
}

func (h *Handlers) buildBusyIntervals(objs []*storage.Object, start, end time.Time) []ical.Interval {
	var busy []ical.Interval
	expander := ical.NewRecurrenceExpander(time.UTC)

	for _, o := range objs {
		if o.Component != "VEVENT" {
			continue
		}

		events, err := ical.ParseCalendar([]byte(o.Data))
		if err != nil {
			if interval := h.extractFallbackInterval(o, start, end); interval != nil {
				busy = append(busy, *interval)
			}
			continue
		}

		expandedEvents, err := expander.ExpandRecurrences(events, start, end)
		if err != nil {
			// Fallback to stored start/end times if expansion fails
			if interval := h.extractFallbackInterval(o, start, end); interval != nil {
				busy = append(busy, *interval)
			}
			continue
		}

		// Convert expanded events to intervals
		for _, event := range expandedEvents {
			if interval := h.eventToInterval(event, start, end); interval != nil {
				busy = append(busy, *interval)
			}
		}
	}

	return common.MergeIntervalsFB(busy)
}

func (h *Handlers) extractFallbackInterval(o *storage.Object, start, end time.Time) *ical.Interval {
	if o.StartAt == nil || o.EndAt == nil {
		return nil
	}

	if o.EndAt.After(start) && (end.After(*o.StartAt) || end.Equal(*o.StartAt)) {
		s := common.MaxTime(*o.StartAt, start)
		e := common.MinTime(*o.EndAt, end)
		if e.After(s) {
			return &ical.Interval{S: s, E: e}
		}
	}
	return nil
}

func (h *Handlers) eventToInterval(event *ical.Event, start, end time.Time) *ical.Interval {
	if event.End.After(start) && (end.After(event.Start) || end.Equal(event.Start)) {
		s := common.MaxTime(event.Start, start)
		e := common.MinTime(event.End, end)
		if e.After(s) {
			return &ical.Interval{S: s, E: e}
		}
	}
	return nil
}
