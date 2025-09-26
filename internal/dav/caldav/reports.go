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

	if strings.HasSuffix(calURI, "-inbox") {
		h.reportSchedulingInbox(w, r, owner, calURI, q)
		return
	}
	if strings.HasSuffix(calURI, "-outbox") {
		h.reportSchedulingOutbox(w, r, owner, calURI, q)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to resolve calendar in calendar-query")
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
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Msg("failed to list objects in calendar-query")
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
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for calendar-query")
	}
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
			h.logger.Debug().Err(err).
				Str("owner", owner).
				Str("calendar", calURI).
				Msg("failed to resolve calendar in multiget")
			continue
		}
		pr := common.MustPrincipal(r.Context())
		okRead, err := h.aclCheckRead(r.Context(), pr, calURI, calOwner)
		if err != nil || !okRead {
			h.logger.Debug().Err(err).
				Bool("can_read", okRead).
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("ACL check failed in multiget")
			continue
		}
		o, err := h.store.GetObject(r.Context(), calendarID, uid)
		if err != nil {
			h.logger.Debug().Err(err).
				Str("calendarID", calendarID).
				Str("uid", uid).
				Msg("failed to get object in multiget")
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
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for calendar-multiget")
	}
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
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to resolve calendar in sync-collection")
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
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Msg("failed to get sync info")
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
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Int64("since", sinceSeq).
			Int("limit", limit).
			Msg("failed to list changes")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	baseHref := r.URL.Path
	if !strings.HasSuffix(baseHref, "/") {
		baseHref += "/"
	}

	for _, ch := range changes {
		hrefStr := baseHref + ch.UID + ".ics"
		if ch.Deleted {
			resp := common.Response{
				Hrefs: []common.Href{{Value: hrefStr}},
			}
			resp.Status = &common.Status{Code: http.StatusNotFound}
			resps = append(resps, resp)
		} else {
			resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
			var obj *storage.Object
			var getErr error
			needObject := props.CalendarData || props.GetETag
			if needObject {
				obj, getErr = h.store.GetObject(r.Context(), calendarID, ch.UID)
				if getErr != nil {
					h.logger.Debug().Err(getErr).
						Str("calendarID", calendarID).
						Str("uid", ch.UID).
						Msg("object disappeared between change listing and fetch")
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

	ms := common.MultiStatus{
		Responses: resps,
		SyncToken: curToken,
	}
	if limit > 0 && len(changes) == limit {
		ms.NumberOfMatchesWithinLimits = fmt.Sprintf("%d", len(changes))
	}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for sync-collection")
	}
}

func (h *Handlers) ReportFreeBusyQuery(w http.ResponseWriter, r *http.Request, fb common.FreeBusyQuery) {
	owner, calURI, _ := splitResourcePath(r.URL.Path, h.basePath)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to resolve calendar in free-busy-query")
		http.NotFound(w, r)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	if fb.Time == nil || fb.Time.Start == "" || fb.Time.End == "" {
		h.logger.Error().Msg("free-busy query missing required time-range")
		http.Error(w, "time-range required", http.StatusBadRequest)
		return
	}

	start, err := common.ParseICalTime(fb.Time.Start)
	if err != nil {
		h.logger.Error().Err(err).Str("start", fb.Time.Start).Msg("bad start time in free-busy query")
		http.Error(w, "bad start", http.StatusBadRequest)
		return
	}

	end, err := common.ParseICalTime(fb.Time.End)
	if err != nil {
		h.logger.Error().Err(err).Str("end", fb.Time.End).Msg("bad end time in free-busy query")
		http.Error(w, "bad end", http.StatusBadRequest)
		return
	}

	if !end.After(start) {
		h.logger.Error().
			Time("start", start).
			Time("end", end).
			Msg("invalid time range in free-busy query")
		http.Error(w, "end must be after start", http.StatusBadRequest)
		return
	}

	var busy []ical.Interval
	freeBusyData, err := h.store.GetFreeBusyInfo(r.Context(), owner, start, end)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Msg("failed to get free/busy info")
			// Fall back to calendar object method
		objs, err := h.store.ListObjectsByComponent(r.Context(), calendarID, []string{"VEVENT"}, &start, &end)
		if err != nil {
			h.logger.Error().Err(err).
				Str("calendarID", calendarID).
				Msg("failed to list events for free-busy query")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		busy = h.buildBusyIntervals(objs, start, end)
	} else {
		// Convert stored free/busy info to intervals
		for _, fbInfo := range freeBusyData {
			if fbInfo.BusyType != "FREE" {
				busy = append(busy, ical.Interval{
					S: fbInfo.StartTime,
					E: fbInfo.EndTime,
				})
			}
		}
	}

	busy = common.MergeIntervalsFB(busy)

	icsData := common.BuildFreeBusyICS(start, end, busy, h.cfg.ICS.BuildProdID())
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	if _, err := w.Write(icsData); err != nil {
		h.logger.Error().Err(err).Msg("failed to write free-busy response")
	}
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
			h.logger.Debug().Err(err).
				Str("uid", o.UID).
				Msg("failed to parse calendar, using fallback interval if available")
			if interval := h.extractFallbackInterval(o, start, end); interval != nil {
				busy = append(busy, *interval)
			}
			continue
		}

		expandedEvents, err := expander.ExpandRecurrences(events, start, end)
		if err != nil {
			h.logger.Debug().Err(err).
				Str("uid", o.UID).
				Msg("failed to expand recurrences, using fallback interval if available")
			if interval := h.extractFallbackInterval(o, start, end); interval != nil {
				busy = append(busy, *interval)
			}
			continue
		}

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

func (h *Handlers) reportSchedulingInbox(w http.ResponseWriter, r *http.Request, owner, inboxURI string, q common.CalendarQuery) {
	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Get the scheduling inbox
	inbox, err := h.store.GetSchedulingInbox(r.Context(), owner)
	if err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Msg("failed to get scheduling inbox")
		http.NotFound(w, r)
		return
	}

	// List scheduling objects in the inbox
	schedObjs, err := h.store.ListSchedulingObjects(r.Context(), inbox.ID)
	if err != nil {
		h.logger.Error().Err(err).Str("inboxID", inbox.ID).Msg("failed to list scheduling objects")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	props := common.ParsePropRequest(q.Prop)
	var resps []common.Response

	for _, obj := range schedObjs {
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, inboxURI, obj.UID+".ics")
		resp := h.buildSchedulingObjectResponse(hrefStr, props, obj)
		resps = append(resps, resp)
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for scheduling inbox query")
	}
}

func (h *Handlers) reportSchedulingOutbox(w http.ResponseWriter, r *http.Request, owner, outboxURI string, q common.CalendarQuery) {
	// Scheduling outbox typically doesn't store persistent objects
	// It's mainly used for POST operations
	// Return empty response
	ms := common.MultiStatus{Responses: []common.Response{}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for scheduling outbox query")
	}
}

func (h *Handlers) buildSchedulingObjectResponse(hrefStr string, props common.PropRequest, obj *storage.SchedulingObject) common.Response {
	resp := common.Response{
		Hrefs: []common.Href{{Value: hrefStr}},
	}

	_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/calendar; charset=utf-8"})

	if props.CalendarData {
		type CalendarData struct {
			XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
			Text    string   `xml:",chardata"`
		}
		_ = resp.EncodeProp(http.StatusOK, CalendarData{Text: obj.Data})
	}

	if props.GetETag && obj.ETag != "" {
		_ = resp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(obj.ETag)})
	}

	if !obj.UpdatedAt.IsZero() {
		_ = resp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(obj.UpdatedAt)})
	}

	return resp
}
