package dav

import (
	"encoding/xml"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

type propRequest struct {
	GetETag      bool
	CalendarData bool
}

type propContainer struct {
	XMLName xml.Name `xml:"DAV: prop"`
	Any     []xml.Name
}

type calendarQuery struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:caldav calendar-query"`
	Prop    propContainer  `xml:"DAV: prop"`
	Filter  calendarFilter `xml:"filter"`
}

type calendarMultiget struct {
	XMLName xml.Name      `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget"`
	Prop    propContainer `xml:"DAV: prop"`
	Hrefs   []string      `xml:"DAV: href"`
}

type syncCollection struct {
	XMLName   xml.Name   `xml:"DAV: sync-collection"`
	SyncToken string     `xml:"sync-token"`
	Limit     *syncLimit `xml:"limit,omitempty"`
}

type syncLimit struct {
	NResults int `xml:"nresults"`
}

type calendarFilter struct {
	CompFilter compFilter `xml:"comp-filter"`
}
type compFilter struct {
	Name       string      `xml:"name,attr"`
	CompFilter *compFilter `xml:"comp-filter,omitempty"`
	TimeRange  *timeRange  `xml:"time-range,omitempty"`
}
type timeRange struct {
	Start string `xml:"start,attr,omitempty"`
	End   string `xml:"end,attr,omitempty"`
}

// free-busy REPORT
type freeBusyQuery struct {
	XMLName xml.Name   `xml:"urn:ietf:params:xml:ns:caldav free-busy-query"`
	Time    *timeRange `xml:"time-range"`
}

func (h *Handlers) handleReport(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	_ = r.Body.Close()

	root := struct {
		XMLName xml.Name
	}{}
	if err := xml.Unmarshal(body, &root); err != nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}

	switch root.XMLName.Space + " " + root.XMLName.Local {
	case nsCalDAV + " calendar-query":
		var q calendarQuery
		_ = xml.Unmarshal(body, &q)
		h.reportCalendarQuery(w, r, q)
	case nsCalDAV + " calendar-multiget":
		var mg calendarMultiget
		_ = xml.Unmarshal(body, &mg)
		h.reportCalendarMultiget(w, r, mg)
	case nsDAV + " sync-collection":
		var sc syncCollection
		_ = xml.Unmarshal(body, &sc)
		h.reportSyncCollection(w, r, sc)
	case nsCalDAV + " free-busy-query":
		var fb freeBusyQuery
		_ = xml.Unmarshal(body, &fb)
		h.reportFreeBusyQuery(w, r, fb)
	default:
		http.Error(w, "unsupported REPORT", http.StatusBadRequest)
	}
}

func (h *Handlers) reportCalendarQuery(w http.ResponseWriter, r *http.Request, q calendarQuery) {
	owner, calURI, _ := h.splitCalendarPath(r.URL.Path)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := mustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	props := parsePropRequest(q.Prop)
	var start, end *time.Time
	if tr := extractTimeRange(q.Filter); tr != nil {
		if tr.Start != "" {
			if t, err := parseICalTime(tr.Start); err == nil {
				start = &t
			}
		}
		if tr.End != "" {
			if t, err := parseICalTime(tr.End); err == nil {
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
	var resps []response
	for _, o := range objs {
		hrefStr := joinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
		resps = append(resps, buildReportResponse(hrefStr, props, o))
	}
	writeMultiStatus(w, multistatus{Resp: resps})
}

func (h *Handlers) reportCalendarMultiget(w http.ResponseWriter, r *http.Request, mg calendarMultiget) {
	props := parsePropRequest(mg.Prop)
	var resps []response
	for _, hrefStr := range mg.Hrefs {
		owner, calURI, rest := h.splitCalendarPath(hrefStr)
		if owner == "" || len(rest) == 0 {
			continue
		}
		filename := rest[len(rest)-1]
		uid := strings.TrimSuffix(filename, filepath.Ext(filename))
		calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
		if err != nil {
			continue
		}
		pr := mustPrincipal(r.Context())
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
	writeMultiStatus(w, multistatus{Resp: resps})
}

func buildReportResponse(hrefStr string, props propRequest, o *storage.Object) response {
	p := prop{}
	p.ContentType = calContentType()
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
	return response{
		Href: hrefStr,
		Prop: propstat{
			Prop:   p,
			Status: ok(),
		},
	}
}

// Sync with paging

func (h *Handlers) reportSyncCollection(w http.ResponseWriter, r *http.Request, sc syncCollection) {
	owner, calURI, _ := h.splitCalendarPath(r.URL.Path)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := mustPrincipal(r.Context())
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

	colHref := h.calendarPath(owner, calURI)
	resps := []response{
		{
			Href: colHref,
			Prop: propstat{
				Prop: prop{
					Resourcetype:                  makeCalendarResourcetype(),
					SupportedCalendarComponentSet: &supportedCompSet{Comp: []comp{{Name: "VEVENT"}, {Name: "VTODO"}, {Name: "VJOURNAL"}}},
					SyncToken:                     &curToken,
				},
				Status: ok(),
			},
		},
	}

	if limit > 0 && len(changes) == limit {
		n := len(changes)
		resps[0].Extra = append(resps[0].Extra, propstat{
			Prop:   prop{MatchesWithinLimits: &n},
			Status: ok(),
		})
	}

	for _, ch := range changes {
		hrefStr := joinURL(h.basePath, "calendars", owner, calURI, ch.UID+".ics")
		if ch.Deleted {
			resps = append(resps, response{
				Href: hrefStr,
				Prop: propstat{
					Prop:   prop{},
					Status: "HTTP/1.1 404 Not Found",
				},
			})
		} else {
			resps = append(resps, response{
				Href: hrefStr,
				Prop: propstat{
					Prop:   prop{ContentType: calContentType()},
					Status: ok(),
				},
			})
		}
	}

	// NOTE: number-of-matches-within-limits omission explained in previous message.
	writeMultiStatus(w, multistatus{Resp: resps})
}

// Free-busy REPORT (simplified, no recurrence expansion)

func (h *Handlers) reportFreeBusyQuery(w http.ResponseWriter, r *http.Request, fb freeBusyQuery) {
	owner, calURI, _ := h.splitCalendarPath(r.URL.Path)
	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := mustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}

	if fb.Time == nil || fb.Time.Start == "" || fb.Time.End == "" {
		http.Error(w, "time-range required", http.StatusBadRequest)
		return
	}
	start, err := parseICalTime(fb.Time.Start)
	if err != nil {
		http.Error(w, "bad start", http.StatusBadRequest)
		return
	}
	end, err := parseICalTime(fb.Time.End)
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

	var busy []interval
	for _, o := range objs {
		if o.StartAt != nil && o.EndAt != nil {
			if o.EndAt.After(start) && (end.After(*o.StartAt) || end.Equal(*o.StartAt)) {
				s := maxTime(*o.StartAt, start)
				e := minTime(*o.EndAt, end)
				if e.After(s) {
					busy = append(busy, interval{s: s, e: e})
				}
			}
		}
	}
	busy = mergeIntervalsFB(busy)

	var sb strings.Builder
	sb.WriteString("BEGIN:VCALENDAR\r\n")
	sb.WriteString("PRODID:-//example.com//caldav//EN\r\n")
	sb.WriteString("VERSION:2.0\r\n")
	sb.WriteString("BEGIN:VFREEBUSY\r\n")
	sb.WriteString("DTSTART:" + start.UTC().Format("20060102T150405Z") + "\r\n")
	sb.WriteString("DTEND:" + end.UTC().Format("20060102T150405Z") + "\r\n")
	for _, iv := range busy {
		sb.WriteString("FREEBUSY:")
		sb.WriteString(iv.s.UTC().Format("20060102T150405Z"))
		sb.WriteString("/")
		sb.WriteString(iv.e.UTC().Format("20060102T150405Z"))
		sb.WriteString("\r\n")
	}
	sb.WriteString("END:VFREEBUSY\r\n")
	sb.WriteString("END:VCALENDAR\r\n")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = io.WriteString(w, sb.String())
}

// helpers for reports

func extractTimeRange(f calendarFilter) *timeRange {
	c := &f.CompFilter
	for c != nil {
		if c.TimeRange != nil {
			return c.TimeRange
		}
		c = c.CompFilter
	}
	return nil
}

func extractComponentFilterNames(f calendarFilter) []string {
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

func parsePropRequest(_ propContainer) propRequest {
	// Default to returning calendar-data and etag for compatibility
	return propRequest{
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
