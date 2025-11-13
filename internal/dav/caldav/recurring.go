package caldav

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

func (h *Handlers) serveRecurringInstance(w http.ResponseWriter, r *http.Request, calendarID, baseUID string, recurrenceID *time.Time) {
	masterObj, exceptions, err := h.getCompleteRecurringEvent(r.Context(), calendarID, baseUID)
	if err != nil {
		h.logger.Error().Err(err).Str("calendarID", calendarID).Str("baseUID", baseUID).Msg("failed to get master object for recurring instance")
		http.NotFound(w, r)
		return
	}

	// Check for explicit exception first
	if excObj := h.findException(exceptions, recurrenceID); excObj != nil {
		if etag := common.TrimQuotes(r.Header.Get("If-None-Match")); etag != "" && etag == excObj.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.writeObjectResponse(w, excObj)
		return
	}

	// Expand from master
	event := h.expandSingleInstance(masterObj, recurrenceID)
	if event == nil {
		http.NotFound(w, r)
		return
	}

	instanceETag := h.generateInstanceETag(masterObj.ETag, event)
	if etag := common.TrimQuotes(r.Header.Get("If-None-Match")); etag != "" && etag == instanceETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	instanceData, err := ical.SerializeEvent(event)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to serialize event instance")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", `"`+instanceETag+`"`)
	if !masterObj.UpdatedAt.IsZero() {
		w.Header().Set("Last-Modified", masterObj.UpdatedAt.UTC().Format(time.RFC1123))
	}
	_, _ = w.Write(instanceData)
}

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
		exceptionMap := make(map[string]*storage.Object)
		for _, exc := range exceptions {
			if excEvents, err := ical.ParseCalendar([]byte(exc.Data)); err == nil && len(excEvents) > 0 && excEvents[0].RecurrenceID != nil {
				key := excEvents[0].RecurrenceID.Format("20060102T150405Z")
				exceptionMap[key] = exc
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

		// Build responses for each instance
		for _, event := range expandedEvents {
			hrefStr := h.buildInstanceHref(event, owner, calURI)

			// Check if there's an explicit exception
			if event.RecurrenceID != nil {
				key := event.RecurrenceID.Format("20060102T150405Z")
				if excObj, exists := exceptionMap[key]; exists {
					resps = append(resps, buildReportResponse(hrefStr, props, excObj))
					continue
				}
			}

			// Use expanded instance
			instanceETag := h.generateInstanceETag(o.ETag, event)
			instanceObj := h.eventToStorageObject(event, instanceETag, o)
			resps = append(resps, buildReportResponse(hrefStr, props, instanceObj))
		}
	}

	return resps
}

func (h *Handlers) buildInstanceHref(event *ical.Event, owner, calURI string) string {
	if event.RecurrenceID != nil {
		instanceID := event.UID + "-" + event.RecurrenceID.Format("20060102T150405Z")
		return common.JoinURL(h.basePath, "calendars", owner, calURI, instanceID+".ics")
	}
	return common.JoinURL(h.basePath, "calendars", owner, calURI, event.UID+".ics")
}

func (h *Handlers) eventToStorageObject(event *ical.Event, etag string, originalObj *storage.Object) *storage.Object {
	data, err := ical.SerializeEvent(event)
	if err != nil {
		h.logger.Warn().Err(err).Str("uid", event.UID).Msg("failed to serialize event")
		return originalObj
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

func (h *Handlers) handleRecurringInstanceRequest(href string, masterObj *storage.Object, props common.PropRequest) *common.Response {
	recurrenceID, _ := h.parseInstancePath(href)
	if recurrenceID == nil {
		return nil
	}

	event := h.expandSingleInstance(masterObj, recurrenceID)
	if event == nil {
		return nil
	}

	instanceETag := h.generateInstanceETag(masterObj.ETag, event)
	instanceObj := h.eventToStorageObject(event, instanceETag, masterObj)
	resp := buildReportResponse(href, props, instanceObj)
	return &resp
}

func (h *Handlers) parseInstancePath(path string) (*time.Time, string) {
	filename := filepath.Base(path)
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	lastDash := strings.LastIndex(uid, "-")
	if lastDash == -1 || lastDash == len(uid)-1 {
		return nil, uid
	}

	timestampPart := uid[lastDash+1:]
	if len(timestampPart) == 16 && timestampPart[8] == 'T' && timestampPart[15] == 'Z' {
		if recTime, err := time.Parse("20060102T150405Z", timestampPart); err == nil {
			return &recTime, uid[:lastDash]
		}
	}

	return nil, uid
}

func (h *Handlers) findException(exceptions []*storage.Object, recurrenceID *time.Time) *storage.Object {
	for _, exc := range exceptions {
		excEvents, err := ical.ParseCalendar([]byte(exc.Data))
		if err == nil && len(excEvents) > 0 && excEvents[0].RecurrenceID != nil {
			if excEvents[0].RecurrenceID.Equal(*recurrenceID) {
				return exc
			}
		}
	}
	return nil
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

func (h *Handlers) determineModificationType(r *http.Request, recurrenceID *time.Time, event *ical.Event) ical.RecurrenceModification {
	if recurrenceID != nil || (event != nil && event.RecurrenceID != nil) {
		if r.Header.Get("X-Modify-Future") == "true" || r.URL.Query().Get("this-and-future") == "true" {
			return ical.ModifyThisFuture
		}
		return ical.ModifyThis
	}
	return ical.ModifyAll
}

func (h *Handlers) writeStoredEventResponse(w http.ResponseWriter, ctx context.Context, calendarID, uid string, recurrenceID *time.Time, isNew bool) {
	storedObj, err := h.store.GetObject(ctx, calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Msg("stored but failed to retrieve object")
		if isNew {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	responseETag := storedObj.ETag
	if recurrenceID != nil {
		responseETag = h.generateInstanceETag(storedObj.ETag, &ical.Event{RecurrenceID: recurrenceID})
	}

	w.Header().Set("ETag", `"`+responseETag+`"`)
	if isNew {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handlers) getCompleteRecurringEvent(ctx context.Context, calendarID, uid string) (*storage.Object, []*storage.Object, error) {
	master, err := h.store.GetMasterEvent(ctx, calendarID, uid)
	if err != nil {
		obj, err := h.store.GetObject(ctx, calendarID, uid)
		if err != nil {
			return nil, nil, err
		}
		return obj, nil, nil
	}

	exceptions, err := h.store.GetEventExceptions(ctx, calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Str("uid", uid).Msg("failed to get exceptions, returning master only")
		return master, nil, nil
	}

	return master, exceptions, nil
}
