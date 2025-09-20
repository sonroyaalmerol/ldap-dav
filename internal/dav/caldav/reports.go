package caldav

import (
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (h *Handlers) ReportCalendarQuery(w http.ResponseWriter, r *http.Request, q common.CalendarQuery) {
	owner, calURI, _ := h.SplitCalendarPath(r.URL.Path)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	props := parsePropRequest(q.Prop)
	var start, end *time.Time
	if tr := extractTimeRange(q.Filter); tr != nil {
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
	comps := extractComponentFilterNames(q.Filter)
	if len(comps) == 0 {
		comps = []string{"VEVENT", "VTODO", "VJOURNAL"}
	}

	objs, err := h.store.ListObjectsByComponent(r.Context(), calendarID, comps, start, end)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	var resps []common.Response
	for _, o := range objs {
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
		resps = append(resps, buildReportResponse(hrefStr, props, o))
	}
	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

func (h *Handlers) ReportCalendarMultiget(w http.ResponseWriter, r *http.Request, mg common.CalendarMultiget) {
	props := parsePropRequest(mg.Prop)
	var resps []common.Response
	for _, hrefStr := range mg.Hrefs {
		owner, calURI, rest := h.SplitCalendarPath(hrefStr)
		if owner == "" || len(rest) == 0 {
			continue
		}
		filename := rest[len(rest)-1]
		uid := strings.TrimSuffix(filename, filepath.Ext(filename))
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
		resps = append(resps, buildReportResponse(hrefStr, props, o))
	}
	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
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

// Sync with paging
func (h *Handlers) ReportSyncCollection(w http.ResponseWriter, r *http.Request, sc common.SyncCollection) {
	owner, calURI, _ := h.SplitCalendarPath(r.URL.Path)
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
		if ss, ok := parseSeqToken(sc.SyncToken); ok {
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

	colHref := common.CalendarPath(h.basePath, owner, calURI)
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

	// NOTE: number-of-matches-within-limits omission explained in previous message.
	common.WriteMultiStatus(w, common.MultiStatus{Resp: resps})
}

// Free-busy REPORT (simplified, no recurrence expansion)
func (h *Handlers) ReportFreeBusyQuery(w http.ResponseWriter, r *http.Request, fb common.FreeBusyQuery) {
	owner, calURI, _ := h.SplitCalendarPath(r.URL.Path)
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

	// Collect VEVENTs overlapping [start,end]
	objs, err := h.store.ListObjectsByComponent(r.Context(), calendarID, []string{"VEVENT"}, &start, &end)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var busy []common.Interval
	for _, o := range objs {
		if o.StartAt != nil && o.EndAt != nil {
			if o.EndAt.After(start) && (end.After(*o.StartAt) || end.Equal(*o.StartAt)) {
				s := common.MaxTime(*o.StartAt, start)
				e := common.MinTime(*o.EndAt, end)
				if e.After(s) {
					busy = append(busy, common.Interval{S: s, E: e})
				}
			}
		}
	}
	busy = common.MergeIntervalsFB(busy)

	var sb strings.Builder
	sb.WriteString("BEGIN:VCALENDAR\r\n")
	sb.WriteString("PRODID:-//example.com//caldav//EN\r\n")
	sb.WriteString("VERSION:2.0\r\n")
	sb.WriteString("BEGIN:VFREEBUSY\r\n")
	sb.WriteString("DTSTART:" + start.UTC().Format("20060102T150405Z") + "\r\n")
	sb.WriteString("DTEND:" + end.UTC().Format("20060102T150405Z") + "\r\n")
	for _, iv := range busy {
		sb.WriteString("FREEBUSY:")
		sb.WriteString(iv.S.UTC().Format("20060102T150405Z"))
		sb.WriteString("/")
		sb.WriteString(iv.E.UTC().Format("20060102T150405Z"))
		sb.WriteString("\r\n")
	}
	sb.WriteString("END:VFREEBUSY\r\n")
	sb.WriteString("END:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = io.WriteString(w, sb.String())
}

// helpers for reports

func extractTimeRange(f common.CalendarFilter) *common.TimeRange {
	c := &f.CompFilter
	for c != nil {
		if c.TimeRange != nil {
			return c.TimeRange
		}
		c = c.CompFilter
	}
	return nil
}

func extractComponentFilterNames(f common.CalendarFilter) []string {
	names := []string{}
	c := &f.CompFilter
	for c != nil {
		if c.Name != "" {
			switch strings.ToUpper(c.Name) {
			case "VCALENDAR":
				// skip; descend
			case "VEVENT", "VTODO", "VJOURNAL":
				names = append(names, strings.ToUpper(c.Name))
			}
		}
		c = c.CompFilter
	}
	return names
}

func parsePropRequest(_ common.PropContainer) common.PropRequest {
	// Default to returning calendar-data and etag for compatibility
	return common.PropRequest{
		GetETag:      true,
		CalendarData: true,
	}
}

func parseSeqToken(tok string) (int64, bool) {
	tok = strings.TrimSpace(tok)
	if strings.HasPrefix(tok, "seq:") {
		v := strings.TrimPrefix(tok, "seq:")
		if v == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
