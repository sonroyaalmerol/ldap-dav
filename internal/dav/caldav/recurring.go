package caldav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

// handleExceptionOnlyPut handles PUT of just an exception (editing single instance)
func (h *Handlers) handleExceptionOnlyPut(w http.ResponseWriter, r *http.Request, calendarID, uid string, exception *ical.Event, existing *storage.Object) error {
	if exception.UID != uid {
		return fmt.Errorf("UID mismatch: expected %s, got %s", uid, exception.UID)
	}

	if exception.RecurrenceID == nil {
		return fmt.Errorf("exception must have RECURRENCE-ID")
	}

	// Ensure master event exists
	if existing == nil {
		return fmt.Errorf("master event not found")
	}

	// Get master to check if EXDATE needs to be removed
	masterEvents, err := ical.ParseCalendar([]byte(existing.Data))
	if err != nil {
		return fmt.Errorf("failed to parse master: %w", err)
	}

	if len(masterEvents) == 0 {
		return fmt.Errorf("no events in master")
	}

	masterEvent := masterEvents[0]

	// If this recurrence was in EXDATE, remove it (we're "undeleting" it with a custom exception)
	if ical.HasExceptionDate(masterEvent, *exception.RecurrenceID) {
		ical.RemoveExceptionDate(masterEvent, *exception.RecurrenceID)

		// Update master
		updatedMasterData, err := ical.SerializeEvent(masterEvent)
		if err != nil {
			return fmt.Errorf("failed to serialize master: %w", err)
		}

		existing.Data = string(updatedMasterData)
		if err := h.store.PutObject(r.Context(), existing); err != nil {
			return fmt.Errorf("failed to update master: %w", err)
		}
	}

	// Store the exception
	if err := h.storeException(r.Context(), calendarID, uid, exception); err != nil {
		return fmt.Errorf("failed to store exception: %w", err)
	}

	w.Header().Set("ETag", `"`+existing.ETag+`"`)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// storeException stores a single exception instance
func (h *Handlers) storeException(ctx context.Context, calendarID, uid string, exception *ical.Event) error {
	data, err := ical.SerializeEvent(exception)
	if err != nil {
		return fmt.Errorf("failed to serialize exception: %w", err)
	}

	data, _ = ical.EnsureDTStamp(data)

	obj := &storage.Object{
		CalendarID: calendarID,
		UID:        uid,
		Data:       string(data),
		Component:  "VEVENT",
		StartAt:    &exception.Start,
		EndAt:      &exception.End,
	}

	return h.store.PutObject(ctx, obj)
}

func (h *Handlers) buildCompleteEventResponses(ctx context.Context, objs []*storage.Object, props common.PropRequest, owner, calURI string) []common.Response {
	var resps []common.Response

	for _, o := range objs {
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")

		if o.Component == "VEVENT" {
			exceptions, _ := h.store.GetEventExceptions(ctx, o.CalendarID, o.UID)

			if len(exceptions) > 0 {
				combinedData, err := h.combineEventWithExceptions(o, exceptions)
				if err != nil {
					h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to combine event with exceptions")
					resps = append(resps, buildReportResponse(hrefStr, props, o))
					continue
				}

				combinedObj := &storage.Object{
					CalendarID: o.CalendarID,
					UID:        o.UID,
					Data:       combinedData,
					Component:  o.Component,
					ETag:       o.ETag,
					UpdatedAt:  o.UpdatedAt,
					StartAt:    o.StartAt,
					EndAt:      o.EndAt,
				}
				resps = append(resps, buildReportResponse(hrefStr, props, combinedObj))
			} else {
				resps = append(resps, buildReportResponse(hrefStr, props, o))
			}
		} else {
			resps = append(resps, buildReportResponse(hrefStr, props, o))
		}
	}

	return resps
}

// buildExpandedEventResponses returns multiple responses per recurring event (one per instance)
func (h *Handlers) buildExpandedEventResponses(ctx context.Context, objs []*storage.Object, start, end time.Time, props common.PropRequest, owner, calURI string) []common.Response {
	var resps []common.Response

	for _, o := range objs {
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")

		// Non-VEVENT components - return as-is
		if o.Component != "VEVENT" {
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		// Parse the master event
		events, err := ical.ParseCalendar([]byte(o.Data))
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to parse calendar object")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		// Check if recurring
		hasRecurrence := false
		for _, event := range events {
			if event.IsRecurring {
				hasRecurrence = true
				break
			}
		}

		// Non-recurring - return single instance (still need to strip timezone and convert to UTC)
		if !hasRecurrence {
			expandedEvent := h.convertEventToExpandedFormat(events[0])
			instanceData, err := ical.SerializeEventWithoutTimezone(expandedEvent)
			if err != nil {
				h.logger.Warn().Err(err).Msg("failed to serialize non-recurring event")
				resps = append(resps, buildReportResponse(hrefStr, props, o))
				continue
			}

			instanceObj := &storage.Object{
				CalendarID: o.CalendarID,
				UID:        o.UID,
				Data:       string(instanceData),
				Component:  "VEVENT",
				ETag:       o.ETag,
				UpdatedAt:  o.UpdatedAt,
				StartAt:    &expandedEvent.Start,
				EndAt:      &expandedEvent.End,
			}
			resps = append(resps, buildReportResponse(hrefStr, props, instanceObj))
			continue
		}

		// Expand recurrences
		expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
		if err != nil || len(expandedEvents) == 0 {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to expand or no instances in range")
			continue
		}

		// Get exceptions
		exceptions, _ := h.store.GetEventExceptions(ctx, o.CalendarID, o.UID)
		exceptionMap := make(map[string]*ical.Event)
		for _, exc := range exceptions {
			if excEvents, err := ical.ParseCalendar([]byte(exc.Data)); err == nil {
				for _, excEvent := range excEvents {
					if excEvent.RecurrenceID != nil {
						key := excEvent.RecurrenceID.UTC().Format("20060102T150405Z")
						exceptionMap[key] = excEvent
					}
				}
			}
		}

		// Create one response per instance (RFC 4791 Section 7.8.3)
		for _, event := range expandedEvents {
			// Use exception if it exists
			var instanceEvent *ical.Event
			if event.RecurrenceID != nil {
				key := event.RecurrenceID.UTC().Format("20060102T150405Z")
				if exc, exists := exceptionMap[key]; exists {
					instanceEvent = exc
				} else {
					instanceEvent = event
				}
			} else {
				instanceEvent = event
			}

			// Convert to expanded format (RFC 4791 Section 7.9.1)
			expandedInstance := h.convertEventToExpandedFormat(instanceEvent)

			// Serialize without timezone info
			instanceData, err := ical.SerializeEventWithoutTimezone(expandedInstance)
			if err != nil {
				h.logger.Warn().Err(err).Msg("failed to serialize instance")
				continue
			}

			instanceObj := &storage.Object{
				CalendarID: o.CalendarID,
				UID:        o.UID,
				Data:       string(instanceData),
				Component:  "VEVENT",
				ETag:       o.ETag,
				UpdatedAt:  o.UpdatedAt,
				StartAt:    &expandedInstance.Start,
				EndAt:      &expandedInstance.End,
			}

			// Same href for all instances - this is correct per RFC 4791!
			resps = append(resps, buildReportResponse(hrefStr, props, instanceObj))
		}
	}

	return resps
}

// convertEventToExpandedFormat converts an event to expanded format per RFC 4791 Section 7.9.1:
// - Remove recurrence properties (RRULE, RDATE, EXDATE, EXRULE)
// - Convert all times to UTC
// - Set RECURRENCE-ID for instances
func (h *Handlers) convertEventToExpandedFormat(event *ical.Event) *ical.Event {
	expanded := &ical.Event{
		UID:          event.UID,
		Summary:      event.Summary,
		Description:  event.Description,
		Location:     event.Location,
		Status:       event.Status,
		Class:        event.Class,
		Transp:       event.Transp,
		Sequence:     event.Sequence,
		Created:      event.Created,
		LastModified: event.LastModified,
		DtStamp:      event.DtStamp,
		Organizer:    event.Organizer,
		Attendees:    event.Attendees,
		Categories:   event.Categories,
		URL:          event.URL,
		Geo:          event.Geo,
		Priority:     event.Priority,
		Resources:    event.Resources,
		Alarms:       event.Alarms,
		Attachments:  event.Attachments,

		// Convert times to UTC
		Start: event.Start.UTC(),
		End:   event.End.UTC(),

		// Set RECURRENCE-ID if this is an instance
		RecurrenceID: nil,

		// Remove all recurrence properties
		IsRecurring: false,
		RRule:       "",
		RDate:       nil,
		ExDate:      nil,
		// Note: EXRULE is deprecated in RFC 5545, so we don't need to handle it
	}

	// Set RECURRENCE-ID to original start time in UTC
	if event.RecurrenceID != nil {
		recID := event.RecurrenceID.UTC()
		expanded.RecurrenceID = &recID
	} else {
		// For expanded instances from master, set RECURRENCE-ID to the instance start
		recID := expanded.Start
		expanded.RecurrenceID = &recID
	}

	return expanded
}

func (h *Handlers) combineEventWithExceptions(master *storage.Object, exceptions []*storage.Object) (string, error) {
	masterEvents, err := ical.ParseCalendar([]byte(master.Data))
	if err != nil {
		return "", err
	}

	allEvents := make([]*ical.Event, 0, len(masterEvents)+len(exceptions))
	allEvents = append(allEvents, masterEvents...)

	for _, exc := range exceptions {
		excEvents, err := ical.ParseCalendar([]byte(exc.Data))
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", exc.UID).Msg("failed to parse exception")
			continue
		}
		allEvents = append(allEvents, excEvents...)
	}

	return ical.SerializeMultipleEvents(allEvents)
}

func (h *Handlers) expandSingleInstance(masterObj *storage.Object, recurrenceID *time.Time) *ical.Event {
	events, err := ical.ParseCalendar([]byte(masterObj.Data))
	if err != nil {
		return nil
	}

	hasRecurrence := false
	for _, event := range events {
		if event.IsRecurring {
			hasRecurrence = true
			break
		}
	}

	if !hasRecurrence {
		return nil
	}

	start := recurrenceID.Add(-24 * time.Hour)
	end := recurrenceID.Add(24 * time.Hour)

	expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
	if err != nil {
		return nil
	}

	for _, event := range expandedEvents {
		if event.RecurrenceID != nil && event.RecurrenceID.Equal(*recurrenceID) {
			return event
		}
	}

	return nil
}

func (h *Handlers) generateInstanceETag(baseETag string, event *ical.Event) string {
	if event.RecurrenceID != nil {
		return baseETag + "-" + event.RecurrenceID.Format("20060102T150405Z")
	}
	return baseETag
}

func (h *Handlers) writeObjectResponse(w http.ResponseWriter, obj *storage.Object) {
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if !obj.UpdatedAt.IsZero() {
		w.Header().Set("Last-Modified", obj.UpdatedAt.UTC().Format(time.RFC1123))
	}
	_, _ = io.WriteString(w, obj.Data)
}

func (h *Handlers) readAndValidateBody(r *http.Request) ([]byte, error) {
	maxICS := h.cfg.HTTP.MaxICSBytes
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxICS+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}
	_ = r.Body.Close()

	if len(raw) == 0 {
		return nil, fmt.Errorf("empty body")
	}

	if maxICS > 0 && int64(len(raw)) > maxICS {
		return nil, fmt.Errorf("payload too large")
	}

	if fixed, inserted := ical.EnsureDTStamp(raw); inserted {
		raw = fixed
	}

	ics, err := ical.NormalizeICS(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid ical: %w", err)
	}

	_, err = ical.DetectICSComponent(ics)
	if err != nil {
		return nil, fmt.Errorf("unsupported component: %w", err)
	}

	return ics, nil
}

func (h *Handlers) validateETags(r *http.Request, existing *storage.Object, recurrenceID *time.Time) bool {
	wantNew := r.Header.Get("If-None-Match") == "*"
	match := common.TrimQuotes(r.Header.Get("If-Match"))

	if wantNew && existing != nil {
		return false
	}

	if match != "" && existing != nil {
		if recurrenceID != nil {
			// Validate instance ETag
			event := h.expandSingleInstance(existing, recurrenceID)
			if event != nil {
				instanceETag := h.generateInstanceETag(existing.ETag, event)
				return instanceETag == match
			}
			return existing.ETag == match
		}
		return existing.ETag == match
	}

	return true
}
