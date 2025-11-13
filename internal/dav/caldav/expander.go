package caldav

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

func (h *Handlers) buildExpandedEventResponses(ctx context.Context, objs []*storage.Object, start, end time.Time, props common.PropRequest, owner, calURI string) []common.Response {
	var resps []common.Response

	for _, o := range objs {
		if o.Component != "VEVENT" {
			// Non-event objects - return as-is
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		events, err := ical.ParseCalendar([]byte(o.Data))
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to parse calendar object")
			// Fall back to original object
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		// Check if any event in the calendar is recurring
		hasRecurrence := false
		for _, event := range events {
			if event.IsRecurring {
				hasRecurrence = true
				break
			}
		}

		if !hasRecurrence {
			// Non-recurring event - return as-is
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		// Get any explicit exceptions for this recurring event
		exceptions, err := h.store.GetEventExceptions(ctx, o.CalendarID, o.UID)
		if err != nil {
			h.logger.Warn().Err(err).
				Str("uid", o.UID).
				Msg("failed to get exceptions for recurring event in report")
		}

		// Parse exception events to get their recurrence IDs
		exceptionRecurrenceIDs := make(map[string]*storage.Object)
		for _, exc := range exceptions {
			excEvents, err := ical.ParseCalendar([]byte(exc.Data))
			if err == nil && len(excEvents) > 0 && excEvents[0].RecurrenceID != nil {
				key := excEvents[0].RecurrenceID.Format("20060102T150405Z")
				exceptionRecurrenceIDs[key] = exc
			}
		}

		expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to expand recurrences")
			// Fall back to original object
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		for _, event := range expandedEvents {
			// Check if there's an explicit exception for this instance
			if event.RecurrenceID != nil {
				key := event.RecurrenceID.Format("20060102T150405Z")
				if excObj, exists := exceptionRecurrenceIDs[key]; exists {
					// Use the explicit exception instead of expanded instance
					hrefStr := h.buildEventInstanceHref(event, owner, calURI)
					resps = append(resps, buildReportResponse(hrefStr, props, excObj))
					continue
				}
			}

			// Use expanded instance
			hrefStr := h.buildEventInstanceHref(event, owner, calURI)
			instanceETag := h.generateInstanceETag(o.ETag, event)
			instanceObj := h.eventToStorageObject(event, instanceETag, o)
			resps = append(resps, buildReportResponse(hrefStr, props, instanceObj))
		}
	}

	return resps
}

func (h *Handlers) buildEventInstanceHref(event *ical.Event, owner, calURI string) string {
	if event.RecurrenceID != nil {
		instanceID := event.UID + "-" + event.RecurrenceID.Format("20060102T150405Z")
		return common.JoinURL(h.basePath, "calendars", owner, calURI, instanceID+".ics")
	}
	return common.JoinURL(h.basePath, "calendars", owner, calURI, event.UID+".ics")
}

func (h *Handlers) generateInstanceETag(baseETag string, event *ical.Event) string {
	// For recurring instances, create a unique ETag based on master ETag + recurrence time
	if event.RecurrenceID != nil {
		return baseETag + "-" + event.RecurrenceID.Format("20060102T150405Z")
	}
	return baseETag
}

func (h *Handlers) eventToStorageObject(event *ical.Event, etag string, originalObj *storage.Object) *storage.Object {
	data, err := ical.SerializeEvent(event)
	if err != nil {
		h.logger.Warn().Err(err).Str("uid", event.UID).Msg("failed to serialize event")
		return originalObj // Fall back to original
	}

	return &storage.Object{
		CalendarID: originalObj.CalendarID,
		UID:        event.UID,
		Data:       string(data),
		Component:  "VEVENT",
		ETag:       etag,
		UpdatedAt:  originalObj.UpdatedAt,
		StartAt:    &event.Start,
		EndAt:      &event.End,
	}
}

// parseRecurrenceIDFromHref extracts the recurrence-id timestamp from a URL path
func (h *Handlers) parseRecurrenceIDFromHref(href string) (*time.Time, error) {
	filename := filepath.Base(href)
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Look for the pattern: UID-YYYYMMDDTHHMMSSZ
	// Find the last dash followed by what looks like a timestamp
	lastDash := strings.LastIndex(uid, "-")
	if lastDash == -1 || lastDash == len(uid)-1 {
		return nil, nil // Not a recurring instance
	}

	timestampPart := uid[lastDash+1:]

	// Try to parse as iCalendar timestamp
	if len(timestampPart) == 16 && timestampPart[8] == 'T' && timestampPart[15] == 'Z' {
		recTime, err := time.Parse("20060102T150405Z", timestampPart)
		if err != nil {
			return nil, err
		}
		return &recTime, nil
	}

	return nil, nil // Not a valid recurring instance format
}

// extractBaseUIDFromHref gets the base UID by removing the recurrence-id suffix
func (h *Handlers) extractBaseUIDFromHref(href string) string {
	filename := filepath.Base(href)
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	lastDash := strings.LastIndex(uid, "-")
	if lastDash == -1 {
		return uid
	}

	timestampPart := uid[lastDash+1:]

	// Check if this looks like a timestamp
	if len(timestampPart) == 16 && timestampPart[8] == 'T' && timestampPart[15] == 'Z' {
		return uid[:lastDash]
	}

	return uid
}

func (h *Handlers) isRecurringInstanceRequest(href string) bool {
	recID, _ := h.parseRecurrenceIDFromHref(href)
	return recID != nil
}

func (h *Handlers) handleRecurringInstanceRequest(href string, masterObj *storage.Object, props common.PropRequest) *common.Response {
	recurrenceTime, err := h.parseRecurrenceIDFromHref(href)
	if err != nil || recurrenceTime == nil {
		return nil
	}

	events, err := ical.ParseCalendar([]byte(masterObj.Data))
	if err != nil {
		return nil
	}

	// Check if master event is actually recurring
	hasRecurrence := false
	for _, event := range events {
		if event.IsRecurring {
			hasRecurrence = true
			break
		}
	}

	if !hasRecurrence {
		return nil // Not a recurring event
	}

	// Expand a window around the requested recurrence time
	start := recurrenceTime.Add(-24 * time.Hour)
	end := recurrenceTime.Add(24 * time.Hour)

	expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
	if err != nil {
		return nil
	}

	// Find the specific instance
	for _, event := range expandedEvents {
		if event.RecurrenceID != nil && event.RecurrenceID.Equal(*recurrenceTime) {
			instanceETag := h.generateInstanceETag(masterObj.ETag, event)
			instanceObj := h.eventToStorageObject(event, instanceETag, masterObj)
			resp := buildReportResponse(href, props, instanceObj)
			return &resp
		}
	}

	return nil
}
