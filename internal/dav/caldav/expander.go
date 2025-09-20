package caldav

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

func (h *Handlers) buildExpandedEventResponses(objs []*storage.Object, start, end time.Time, props common.PropRequest, owner, calURI string) []common.Response {
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

		expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to expand recurrences")
			// Fall back to original object
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		for _, event := range expandedEvents {
			hrefStr := h.buildEventInstanceHref(event, owner, calURI)

			instanceObj := h.eventToStorageObject(event, o)

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

func (h *Handlers) eventToStorageObject(event *ical.Event, originalObj *storage.Object) *storage.Object {
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
		ETag:       ical.GenerateEventETag(event),
		UpdatedAt:  originalObj.UpdatedAt,
		StartAt:    &event.Start,
		EndAt:      &event.End,
	}
}

func (h *Handlers) extractBaseUID(uid string) string {
	if idx := strings.LastIndex(uid, "-2"); idx > 0 && len(uid) > idx+16 {
		suffix := uid[idx+1:]
		if len(suffix) == 16 && suffix[8] == 'T' && suffix[15] == 'Z' {
			return uid[:idx]
		}
	}
	return uid
}

func (h *Handlers) isRecurringInstanceRequest(href string) bool {
	filename := filepath.Base(href)
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))
	return uid != h.extractBaseUID(uid)
}

func (h *Handlers) handleRecurringInstanceRequest(href string, masterObj *storage.Object, props common.PropRequest) *common.Response {
	filename := filepath.Base(href)
	instanceUID := strings.TrimSuffix(filename, filepath.Ext(filename))

	baseUID := h.extractBaseUID(instanceUID)
	if baseUID == instanceUID {
		return nil // Not a recurring instance
	}

	suffix := instanceUID[len(baseUID)+1:] // Remove base UID and dash
	recurrenceTime, err := time.Parse("20060102T150405Z", suffix)
	if err != nil {
		return nil
	}

	events, err := ical.ParseCalendar([]byte(masterObj.Data))
	if err != nil {
		return nil
	}

	start := recurrenceTime.Add(-24 * time.Hour)
	end := recurrenceTime.Add(24 * time.Hour)

	expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
	if err != nil {
		return nil
	}

	for _, event := range expandedEvents {
		if event.RecurrenceID != nil && event.RecurrenceID.Equal(recurrenceTime) {
			instanceObj := h.eventToStorageObject(event, masterObj)
			resp := buildReportResponse(href, props, instanceObj)
			return &resp
		}
	}

	return nil
}
