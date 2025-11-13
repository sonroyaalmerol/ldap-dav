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

func (h *Handlers) buildExpandedEventResponses(ctx context.Context, objs []*storage.Object, start, end time.Time, props common.PropRequest, owner, calURI string) []common.Response {
	var resps []common.Response

	for _, o := range objs {
		if o.Component != "VEVENT" {
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		events, err := ical.ParseCalendar([]byte(o.Data))
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to parse calendar object")
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		hasRecurrence := false
		for _, event := range events {
			if event.IsRecurring {
				hasRecurrence = true
				break
			}
		}

		if !hasRecurrence {
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		// Get exceptions for this recurring event
		exceptions, _ := h.store.GetEventExceptions(ctx, o.CalendarID, o.UID)
		exceptionMap := make(map[string]*ical.Event)

		for _, exc := range exceptions {
			if excEvents, err := ical.ParseCalendar([]byte(exc.Data)); err == nil {
				for _, excEvent := range excEvents {
					if excEvent.RecurrenceID != nil {
						key := excEvent.RecurrenceID.Format("20060102T150405Z")
						exceptionMap[key] = excEvent
					}
				}
			}
		}

		// Expand recurrences
		expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
		if err != nil {
			h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to expand recurrences")
			hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")
			resps = append(resps, buildReportResponse(hrefStr, props, o))
			continue
		}

		// For each expanded instance, create a response with the instance data
		// but the SAME href (standard CalDAV approach)
		hrefStr := common.JoinURL(h.basePath, "calendars", owner, calURI, o.UID+".ics")

		for _, event := range expandedEvents {
			// Check if there's an explicit exception for this instance
			var instanceEvent *ical.Event
			if event.RecurrenceID != nil {
				key := event.RecurrenceID.Format("20060102T150405Z")
				if exc, exists := exceptionMap[key]; exists {
					instanceEvent = exc
				} else {
					instanceEvent = event
				}
			} else {
				instanceEvent = event
			}

			// Serialize this specific instance
			instanceData, err := ical.SerializeEvent(instanceEvent)
			if err != nil {
				h.logger.Warn().Err(err).Str("uid", o.UID).Msg("failed to serialize event instance")
				continue
			}

			// Create a temporary object for this instance
			instanceObj := &storage.Object{
				CalendarID: o.CalendarID,
				UID:        o.UID,
				Data:       string(instanceData),
				Component:  "VEVENT",
				ETag:       o.ETag, // Same ETag for all instances (same resource)
				UpdatedAt:  o.UpdatedAt,
				StartAt:    &instanceEvent.Start,
				EndAt:      &instanceEvent.End,
			}

			// Build response with SAME href for all instances
			resps = append(resps, buildReportResponse(hrefStr, props, instanceObj))
		}
	}

	return resps
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
