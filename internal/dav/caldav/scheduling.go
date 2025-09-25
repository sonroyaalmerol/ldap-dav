package caldav

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	intical "github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

func (h *Handlers) HandleSchedulingInboxPropfind(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())

	// List scheduling inbox objects
	messages, err := h.store.GetSchedulingInboxObjects(r.Context(), pr.UserID)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to get scheduling inbox objects")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	// Inbox collection response
	inboxResp := common.Response{
		Hrefs: []common.Href{{Value: r.URL.Path}},
	}
	_ = inboxResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
	_ = inboxResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Scheduling Inbox"})
	resps = append(resps, inboxResp)

	// Individual message responses
	for _, msg := range messages {
		hrefStr := r.URL.Path + "/" + msg.UID + ".ics"
		msgResp := common.Response{
			Hrefs: []common.Href{{Value: hrefStr}},
		}
		_ = msgResp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/calendar; charset=utf-8"})
		resps = append(resps, msgResp)
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for scheduling inbox")
	}
}

func (h *Handlers) HandleSchedulingInboxGet(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())

	// Extract UID from path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) == 0 {
		http.NotFound(w, r)
		return
	}

	filename := pathParts[len(pathParts)-1]
	uid := strings.TrimSuffix(filename, ".ics")

	messages, err := h.store.GetSchedulingInboxObjects(r.Context(), pr.UserID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for _, msg := range messages {
		if msg.UID == uid {
			w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
			w.Write([]byte(msg.Data))
			return
		}
	}

	http.NotFound(w, r)
}

func (h *Handlers) HandleSchedulingInboxDelete(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())

	// Extract UID from path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) == 0 {
		http.NotFound(w, r)
		return
	}

	filename := pathParts[len(pathParts)-1]
	uid := strings.TrimSuffix(filename, ".ics")

	if err := h.store.DeleteSchedulingInboxObject(r.Context(), pr.UserID, uid); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) HandleSchedulingOutboxPropfind(w http.ResponseWriter, r *http.Request) {
	outboxResp := common.Response{
		Hrefs: []common.Href{{Value: r.URL.Path}},
	}
	_ = outboxResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
	_ = outboxResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Scheduling Outbox"})

	ms := common.MultiStatus{Responses: []common.Response{outboxResp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for scheduling outbox")
	}
}

// HandleSchedulingOutboxPost implements RFC 6638 outbox POST for free-busy requests.
// Expected:
//   - Header "Originator: <mailto:user@example.com>"
//   - One or more "Recipient: <mailto:other@example.com>" headers (can be repeated or comma-separated)
//   - Body: text/calendar iTIP with METHOD:REQUEST and a VFREEBUSY component with DTSTART/DTEND
//
// Response:
//   - application/xml CalDAV schedule-response with per-recipient status and optional calendar-data
func (h *Handlers) HandleSchedulingOutboxPost(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())

	// Verify this is posting to the current user's outbox
	owner, collection, _ := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || collection != "outbox" {
		h.logger.Error().
			Str("path", r.URL.Path).
			Str("owner", owner).
			Str("collection", collection).
			Msg("POST to non-outbox path")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if pr == nil || pr.UserID == "" || pr.UserID != owner {
		h.logger.Debug().
			Str("user", pr.UserID).
			Str("owner", owner).
			Msg("outbox POST forbidden - user mismatch")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Headers: Originator and Recipient (RFC 6638 §8.1)
	originator := parseCalendarUserHeader(r.Header.Get("Originator"))
	if originator == "" {
		h.logger.Debug().Msg("missing Originator header in outbox POST")
		http.Error(w, "Originator required", http.StatusBadRequest)
		return
	}
	recipients := parseMultiCalendarUserHeaders(r.Header["Recipient"])
	if len(recipients) == 0 {
		h.logger.Debug().Msg("no Recipient headers in outbox POST")
		http.Error(w, "Recipient required", http.StatusBadRequest)
		return
	}

	// Body: text/calendar with METHOD:REQUEST and VFREEBUSY
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20)) // 2 MB
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read outbox POST body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	_, vb, method, err := parseITIPVFreeBusy(body)
	if err != nil {
		h.logger.Debug().Err(err).Msg("invalid iTIP VFREEBUSY in outbox POST")
		http.Error(w, "unsupported scheduling message", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(method, "REQUEST") {
		h.logger.Debug().Str("method", method).Msg("only METHOD:REQUEST supported in outbox POST")
		http.Error(w, "only METHOD:REQUEST supported", http.StatusNotImplemented)
		return
	}
	if vb == nil {
		h.logger.Debug().Msg("no VFREEBUSY component in outbox POST")
		http.Error(w, "VFREEBUSY required", http.StatusBadRequest)
		return
	}

	// Extract time-range from VFREEBUSY (DTSTART/DTEND required)
	start, end, err := extractVFreeBusyTimeRange(vb)
	if err != nil {
		h.logger.Debug().Err(err).Msg("invalid VFREEBUSY time-range")
		http.Error(w, "invalid VFREEBUSY time-range", http.StatusBadRequest)
		return
	}
	if !end.After(start) {
		http.Error(w, "end must be after start", http.StatusBadRequest)
		return
	}

	// For each recipient:
	// - if internal user (found in directory), compute free-busy from all of their calendars
	// - otherwise, return 3.7;Invalid calendar user (internal-only implementation)
	var responses []common.ScheduleRecipient

	for _, rcpt := range recipients {
		rcptEmail := strings.TrimPrefix(rcpt, "mailto:")
		// Directory lookup for internal users
		usr, err := h.dir.LookupUserByAttr(r.Context(), "mail", rcptEmail)
		if err != nil || usr == nil || usr.UID == "" {
			// Not an internal user in this implementation
			responses = append(responses, common.ScheduleRecipient{
				Recipient:     rcpt,
				RequestStatus: "3.7;Invalid calendar user",
			})
			continue
		}

		// Aggregate free-busy across all of the user's calendars
		cals, err := h.store.ListCalendarsByOwnerUser(r.Context(), usr.UID)
		if err != nil {
			h.logger.Error().Err(err).Str("user", usr.UID).Msg("failed to list calendars for free-busy")
			responses = append(responses, common.ScheduleRecipient{
				Recipient:     rcpt,
				RequestStatus: "5.1;Service unavailable",
			})
			continue
		}

		var allObjs []*storage.Object
		for _, c := range cals {
			objs, err := h.store.ListObjectsByComponent(r.Context(), c.ID, []string{"VEVENT"}, &start, &end)
			if err != nil {
				h.logger.Debug().Err(err).
					Str("calendarID", c.ID).
					Str("recipient", rcptEmail).
					Msg("failed to list events for recipient")
				continue
			}
			allObjs = append(allObjs, objs...)
		}

		busy := h.buildBusyIntervals(allObjs, start, end)
		icsData := common.BuildFreeBusyICS(start, end, busy, h.cfg.ICS.BuildProdID())
		icsDataStr := string(icsData)

		responses = append(responses, common.ScheduleRecipient{
			Recipient:     rcpt,
			RequestStatus: "2.0;Success",
			CalendarData:  &icsDataStr,
		})
	}

	// Return schedule-response XML
	w.Header().Set("Content-Type", "application/xml; charset=\"utf-8\"")
	w.WriteHeader(http.StatusOK)
	enc := xml.NewEncoder(w)
	_, _ = w.Write([]byte(xml.Header))
	if err := enc.Encode(common.ScheduleResponse{Response: responses}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode schedule-response")
	}
}

// parseCalendarUserHeader parses a single calendar user header value which may contain
// optional angle brackets, e.g., "<mailto:user@example.com>".
func parseCalendarUserHeader(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	// Strip optional angle brackets
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		s = strings.TrimPrefix(s, "<")
		s = strings.TrimSuffix(s, ">")
	}
	return s
}

// parseMultiCalendarUserHeaders parses multiple Recipient headers. A header can
// appear multiple times, and individual headers can contain comma-separated values.
// Each value may be wrapped in angle brackets.
func parseMultiCalendarUserHeaders(values []string) []string {
	var out []string
	for _, v := range values {
		parts := strings.Split(v, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			p = parseCalendarUserHeader(p)
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// parseITIPVFreeBusy parses the posted iTIP calendar and returns the calendar,
// the VFREEBUSY component (if present), the METHOD value, and an error if invalid.
func parseITIPVFreeBusy(data []byte) (*ical.Calendar, *ical.Component, string, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse error: %w", err)
	}
	method := ""
	if mp := cal.Props.Get(ical.PropMethod); mp != nil {
		method = mp.Value
	}
	var vb *ical.Component
	for _, comp := range cal.Children {
		if comp.Name == ical.CompFreeBusy {
			vb = comp
			break
		}
	}
	if vb == nil {
		return cal, nil, method, fmt.Errorf("no VFREEBUSY component")
	}
	return cal, vb, method, nil
}

func extractVFreeBusyTimeRange(vb *ical.Component) (time.Time, time.Time, error) {
	dtstart := vb.Props.Get(ical.PropDateTimeStart)
	dtend := vb.Props.Get(ical.PropDateTimeEnd)
	if dtstart == nil || dtend == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("DTSTART/DTEND required")
	}
	start, _, err := intical.ParseDateTime(dtstart.Value)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("bad DTSTART: %w", err)
	}
	end, _, err := intical.ParseDateTime(dtend.Value)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("bad DTEND: %w", err)
	}
	return start.UTC(), end.UTC(), nil
}
