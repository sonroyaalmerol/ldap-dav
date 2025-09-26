package caldav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	intical "github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

type SchedulingMessage struct {
	Method      string // REQUEST, REPLY, CANCEL, etc.
	UID         string
	Organizer   string
	Attendees   []string
	OriginalICS string
	Events      []*intical.Event
}

// TODO: Implement event attendees list
// TODO: Actual processing of inbox scheduling objects
// TODO: Actual processing of invitation requests
// TODO: Add periodic scheduling cleanup task

func (h *Handlers) parseSchedulingMessage(data []byte) (*SchedulingMessage, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to parse calendar: %w", err)
	}

	method := ""
	if methodProp := cal.Props.Get(ical.PropMethod); methodProp != nil {
		method = methodProp.Value
	}

	events, err := intical.ParseCalendar(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse events: %w", err)
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("no events found in calendar")
	}

	firstEvent := events[0]

	msg := &SchedulingMessage{
		Method:      method,
		UID:         firstEvent.UID,
		OriginalICS: string(data),
		Events:      events,
	}

	msg.Organizer, msg.Attendees = h.extractOrganizerAndAttendees(cal)

	return msg, nil
}

func (h *Handlers) extractOrganizerAndAttendees(cal *ical.Calendar) (organizer string, attendees []string) {
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}

		if orgProp := comp.Props.Get(ical.PropOrganizer); orgProp != nil {
			organizer = strings.TrimPrefix(orgProp.Value, "mailto:")
		}

		for _, attendeeProp := range comp.Props.Values(ical.PropAttendee) {
			attendeeEmail := strings.TrimPrefix(attendeeProp.Value, "mailto:")
			attendees = append(attendees, attendeeEmail)
		}

		break
	}

	return organizer, attendees
}

func (h *Handlers) handleSchedulingOutboxPost(w http.ResponseWriter, r *http.Request, owner, outboxURI string) {
	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		h.logger.Debug().
			Str("user", pr.UserID).
			Str("owner", owner).
			Msg("insufficient privileges for scheduling outbox POST")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	maxICS := h.cfg.HTTP.MaxICSBytes
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxICS+1))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read scheduling POST body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if maxICS > 0 && int64(len(raw)) > maxICS {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	schedMsg, err := h.parseSchedulingMessage(raw)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to parse scheduling message")
		http.Error(w, "bad scheduling message", http.StatusBadRequest)
		return
	}

	if schedMsg.Method == "" {
		h.logger.Error().Msg("missing METHOD in scheduling request")
		http.Error(w, "missing METHOD", http.StatusBadRequest)
		return
	}

	responses, err := h.processSchedulingRequest(r.Context(), schedMsg)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to process scheduling request")
		http.Error(w, "scheduling failed", http.StatusInternalServerError)
		return
	}

	// Return the scheduling response
	h.serveSchedulingResponse(w, responses)
}

func (h *Handlers) processSchedulingRequest(ctx context.Context, schedMsg *SchedulingMessage) ([]SchedulingResponse, error) {
	var responses []SchedulingResponse

	switch schedMsg.Method {
	case "REQUEST":
		responses = h.handleSchedulingRequest(ctx, schedMsg)
	case "REPLY":
		responses = h.handleSchedulingReply(ctx, schedMsg)
	case "CANCEL":
		responses = h.handleSchedulingCancel(ctx, schedMsg)
	default:
		return nil, fmt.Errorf("unsupported method: %s", schedMsg.Method)
	}

	return responses, nil
}

func (h *Handlers) handleSchedulingRequest(ctx context.Context, schedMsg *SchedulingMessage) []SchedulingResponse {
	var responses []SchedulingResponse

	for _, attendee := range schedMsg.Attendees {
		response := SchedulingResponse{
			Recipient: attendee,
			Status:    "2.0", // Success by default
		}

		if err := h.deliverToInbox(ctx, attendee, schedMsg); err != nil {
			h.logger.Error().Err(err).
				Str("attendee", attendee).
				Str("uid", schedMsg.UID).
				Msg("failed to deliver invitation")
			response.Status = "3.7" // Invalid calendar user
			response.Error = err.Error()
		}

		responses = append(responses, response)
	}

	return responses
}

func (h *Handlers) handleSchedulingReply(ctx context.Context, schedMsg *SchedulingMessage) []SchedulingResponse {
	var responses []SchedulingResponse

	partstat := h.extractParticipationStatus(schedMsg)

	response := &storage.AttendeeResponse{
		EventUID:       schedMsg.UID,
		AttendeeEmail:  schedMsg.Organizer, // In REPLY, organizer is the sender
		ResponseStatus: partstat,
		ResponseData:   schedMsg.OriginalICS,
	}

	if err := h.store.StoreAttendeeResponse(ctx, response); err != nil {
		h.logger.Error().Err(err).
			Str("uid", schedMsg.UID).
			Msg("failed to store attendee response")
		responses = append(responses, SchedulingResponse{
			Recipient: schedMsg.Organizer,
			Status:    "5.0", // Server error
			Error:     err.Error(),
		})
	} else {
		responses = append(responses, SchedulingResponse{
			Recipient: schedMsg.Organizer,
			Status:    "2.0", // Success
		})
	}

	return responses
}

func (h *Handlers) handleSchedulingCancel(ctx context.Context, schedMsg *SchedulingMessage) []SchedulingResponse {
	var responses []SchedulingResponse

	for _, attendee := range schedMsg.Attendees {
		response := SchedulingResponse{
			Recipient: attendee,
			Status:    "2.0",
		}

		// Deliver cancellation to attendee's inbox
		if err := h.deliverCancellationToInbox(ctx, attendee, schedMsg); err != nil {
			response.Status = "3.7"
			response.Error = err.Error()
		}

		responses = append(responses, response)
	}

	return responses
}

func (h *Handlers) extractParticipationStatus(schedMsg *SchedulingMessage) string {
	// Parse the raw calendar to get PARTSTAT parameter
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(schedMsg.OriginalICS))).Decode()
	if err != nil {
		return "NEEDS-ACTION"
	}

	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}

		for _, attendeeProp := range comp.Props.Values(ical.PropAttendee) {
			if partstat := attendeeProp.Params.Get(ical.ParamParticipationStatus); partstat != "" {
				return partstat
			}
		}
	}

	return "NEEDS-ACTION"
}

func (h *Handlers) deliverToInbox(ctx context.Context, attendee string, schedMsg *SchedulingMessage) error {
	inbox, err := h.store.GetSchedulingInbox(ctx, attendee)
	if err != nil {
		if err := h.store.CreateSchedulingInbox(ctx, attendee, ""); err != nil {
			return fmt.Errorf("failed to create/get inbox for %s: %w", attendee, err)
		}
		inbox, err = h.store.GetSchedulingInbox(ctx, attendee)
		if err != nil {
			return fmt.Errorf("failed to get inbox after creation for %s: %w", attendee, err)
		}
	}

	schedObj := &storage.SchedulingObject{
		CalendarID: inbox.ID,
		UID:        schedMsg.UID,
		Data:       schedMsg.OriginalICS,
		Method:     schedMsg.Method,
		Recipient:  attendee,
		Originator: schedMsg.Organizer,
		Status:     "pending",
	}

	return h.store.StoreSchedulingObject(ctx, schedObj)
}

func (h *Handlers) deliverCancellationToInbox(ctx context.Context, attendee string, schedMsg *SchedulingMessage) error {
	inbox, err := h.store.GetSchedulingInbox(ctx, attendee)
	if err != nil {
		return fmt.Errorf("failed to get inbox for %s: %w", attendee, err)
	}

	schedObj := &storage.SchedulingObject{
		CalendarID: inbox.ID,
		UID:        schedMsg.UID,
		Data:       schedMsg.OriginalICS,
		Method:     "CANCEL",
		Recipient:  attendee,
		Originator: schedMsg.Organizer,
		Status:     "pending",
	}

	return h.store.StoreSchedulingObject(ctx, schedObj)
}

type SchedulingResponse struct {
	Recipient string
	Status    string // iTIP status codes: 2.0 = success, 3.7 = invalid user, 5.0 = server error
	Error     string
}

func (h *Handlers) serveSchedulingResponse(w http.ResponseWriter, responses []SchedulingResponse) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	xml := `<?xml version="1.0" encoding="UTF-8"?>
<C:schedule-response xmlns:C="urn:ietf:params:xml:ns:caldav">
`
	for _, resp := range responses {
		xml += fmt.Sprintf(`  <C:response>
    <C:recipient>
      <D:href xmlns:D="DAV:">mailto:%s</D:href>
    </C:recipient>
    <C:request-status>%s</C:request-status>
  </C:response>
`, resp.Recipient, resp.Status)
	}
	xml += `</C:schedule-response>`

	w.Write([]byte(xml))
}

func (h *Handlers) updateFreeBusyInfo(ctx context.Context, calendarURI string, obj *storage.Object) error {
	cal, err := h.store.GetCalendarByURI(ctx, calendarURI)
	if err != nil {
		return err
	}

	// Parse the event to extract time information
	events, err := intical.ParseCalendar([]byte(obj.Data))
	if err != nil {
		return err
	}

	for _, event := range events {
		if event.Start.IsZero() || event.End.IsZero() {
			continue
		}

		freeBusyInfo := &storage.FreeBusyInfo{
			UserID:    cal.OwnerUserID,
			StartTime: event.Start,
			EndTime:   event.End,
			BusyType:  "BUSY", // Could be BUSY, BUSY-UNAVAILABLE, BUSY-TENTATIVE
			EventUID:  event.UID,
		}

		if err := h.store.StoreFreeBusyInfo(ctx, freeBusyInfo); err != nil {
			return err
		}
	}

	return nil
}

func (h *Handlers) deleteFreeBusyInfoForEvent(ctx context.Context, calendarURI, eventUID string) error {
	// Get calendar owner
	cal, err := h.store.GetCalendarByURI(ctx, calendarURI)
	if err != nil {
		return err
	}

	return h.store.DeleteFreeBusyInfo(ctx, cal.OwnerUserID, eventUID)
}
