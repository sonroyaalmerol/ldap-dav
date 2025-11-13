package caldav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

func (h *Handlers) GetCapabilities() string {
	return "calendar-access"
}

func (h *Handlers) HandleHead(w http.ResponseWriter, r *http.Request) {
	hrw := &headResponseWriter{ResponseWriter: w}
	h.HandleGet(hrw, r)
}

func (h *Handlers) HandleGet(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || len(rest) == 0 {
		h.logger.Debug().Str("path", r.URL.Path).Msg("GET request with invalid path")
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		h.logger.Error().Str("calendar", calURI).Str("uid", uid).Msg("GET request with unsafe path segments")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to resolve calendar in GET")
		http.NotFound(w, r)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if pr.UserID != calOwner {
		if !h.checkReadAccess(w, r.Context(), pr, calURI) {
			return
		}
	}

	// Parse potential recurring instance path
	recurrenceID, baseUID := h.parseInstancePath(r.URL.Path)
	if recurrenceID != nil {
		h.serveRecurringInstance(w, r, calendarID, baseUID, recurrenceID)
		return
	}

	// Serve regular object
	h.serveObject(w, r, calendarID, uid)
}

func (h *Handlers) serveObject(w http.ResponseWriter, r *http.Request, calendarID, uid string) {
	obj, err := h.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		h.logger.Error().Err(err).Str("calendarID", calendarID).Str("uid", uid).Msg("failed to get object in GET")
		http.NotFound(w, r)
		return
	}

	if etag := common.TrimQuotes(r.Header.Get("If-None-Match")); etag != "" && etag == obj.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	h.writeObjectResponse(w, obj)
}

func (h *Handlers) HandlePut(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || len(rest) == 0 {
		h.logger.Debug().Str("path", r.URL.Path).Msg("PUT request with invalid path")
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	if !strings.HasSuffix(strings.ToLower(filename), ".ics") {
		h.logger.Error().Str("filename", filename).Msg("PUT request with invalid filename")
		http.Error(w, "bad object name", http.StatusBadRequest)
		return
	}
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		h.logger.Error().Str("calendar", calURI).Str("uid", uid).Msg("PUT request with unsafe path segments")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to resolve calendar in PUT")
		http.NotFound(w, r)
		return
	}

	pr := common.MustPrincipal(r.Context())

	// Parse potential recurring instance path
	recurrenceID, baseUID := h.parseInstancePath(r.URL.Path)
	lookupUID := baseUID

	existing, _ := h.store.GetObject(r.Context(), calendarID, lookupUID)

	// Check permissions
	if pr.UserID != calOwner {
		if !h.checkWriteAccess(w, r.Context(), pr, calURI, existing) {
			return
		}
	}

	// Read and validate body
	raw, err := h.readAndValidateBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse calendar
	events, err := ical.ParseCalendar(raw)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to parse incoming calendar")
		http.Error(w, "invalid ical", http.StatusBadRequest)
		return
	}

	if len(events) == 0 {
		h.logger.Error().Msg("no events found in calendar")
		http.Error(w, "no events", http.StatusBadRequest)
		return
	}

	incomingEvent := events[0]
	incomingEvent.UID = lookupUID

	// Validate ETags
	if !h.validateETags(r, existing, recurrenceID) {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}

	// Determine modification type
	modType := h.determineModificationType(r, recurrenceID, incomingEvent)

	// Prepare and store events
	req := &ical.EventPutRequest{
		Event:                incomingEvent,
		ModificationType:     modType,
		OriginalRecurrenceID: recurrenceID,
		PreserveExceptions:   existing != nil,
	}

	eventsToStore, err := h.recurrenceManager.PrepareEventPut(req)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to prepare event for storage")
		http.Error(w, fmt.Sprintf("failed to prepare event: %v", err), http.StatusBadRequest)
		return
	}

	if len(eventsToStore) == 0 {
		h.logger.Error().Msg("no events to store after preparation")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.storeEvents(r.Context(), calendarID, eventsToStore); err != nil {
		h.logger.Error().Err(err).Msg("failed to store events")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	// Return response
	h.writeStoredEventResponse(w, r.Context(), calendarID, lookupUID, recurrenceID, existing == nil)
}

func (h *Handlers) HandleDelete(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if owner == "" || calURI == "" {
		if o2, c2, ok := tryCalendarShorthand(r.URL.Path, h.basePath, pr.UserID); ok {
			owner, calURI, rest = o2, c2, nil
		}
	}

	if owner == "" || calURI == "" {
		h.logger.Error().Str("path", r.URL.Path).Str("owner", owner).Str("calendar", calURI).Msg("DELETE request with invalid path")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	// Delete calendar collection
	if len(rest) == 0 {
		h.deleteCalendar(w, owner, calURI, pr)
		return
	}

	// Delete object
	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		h.logger.Error().Str("calendar", calURI).Str("uid", uid).Msg("unsafe path segments in DELETE object")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to resolve calendar in DELETE")
		http.NotFound(w, r)
		return
	}

	if pr.UserID != calOwner {
		if !h.checkUnbindAccess(w, r.Context(), pr, calURI) {
			return
		}
	}

	// Parse potential recurring instance path
	recurrenceID, baseUID := h.parseInstancePath(r.URL.Path)

	existing, err := h.store.GetObject(r.Context(), calendarID, baseUID)
	if err != nil {
		h.logger.Error().Err(err).Msg("object not found")
		http.NotFound(w, r)
		return
	}

	// Validate ETag
	if !h.validateETags(r, existing, recurrenceID) {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}

	// Determine modification type
	modType := h.determineModificationType(r, recurrenceID, nil)

	// Handle delete based on type
	if modType == ical.ModifyAll && recurrenceID == nil {
		h.deleteEntireEvent(r.Context(), calendarID, baseUID)
	} else {
		h.deleteRecurringInstance(r.Context(), calendarID, baseUID, recurrenceID, modType, existing)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) deleteCalendar(w http.ResponseWriter, owner, calURI string, pr *auth.Principal) {
	if !common.SafeCollectionName(calURI) {
		h.logger.Error().Str("calendar", calURI).Msg("unsafe collection name in DELETE")
		http.Error(w, "bad collection name", http.StatusBadRequest)
		return
	}

	if pr.UserID != owner {
		h.logger.Debug().Str("user", pr.UserID).Str("calendar", calURI).Msg("insufficient privileges for DELETE calendar")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.DeleteCalendar(owner, calURI); err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to delete calendar")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) deleteEntireEvent(ctx context.Context, calendarID, uid string) error {
	exceptions, err := h.store.GetEventExceptions(ctx, calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Str("uid", uid).Msg("failed to get exceptions before delete")
	}

	// Delete all exceptions
	for _, exc := range exceptions {
		if err := h.store.DeleteObject(ctx, calendarID, exc.UID, ""); err != nil {
			h.logger.Warn().Err(err).Str("uid", exc.UID).Msg("failed to delete exception")
		}
	}

	// Delete master
	return h.store.DeleteEventInstance(ctx, calendarID, uid, nil)
}

func (h *Handlers) deleteRecurringInstance(ctx context.Context, calendarID, uid string, recurrenceID *time.Time, modType ical.RecurrenceModification, existing *storage.Object) error {
	events, err := ical.ParseCalendar([]byte(existing.Data))
	if err != nil {
		return err
	}

	if len(events) == 0 {
		return fmt.Errorf("no events in calendar data")
	}

	deleteReq := &ical.EventDeleteRequest{
		UID:              uid,
		ModificationType: modType,
		RecurrenceID:     recurrenceID,
	}

	modifiedMaster, err := h.recurrenceManager.PrepareEventDelete(deleteReq, events[0])
	if err != nil {
		return err
	}

	if modifiedMaster == nil {
		return h.store.DeleteEventInstance(ctx, calendarID, uid, nil)
	}

	return h.storeEvents(ctx, calendarID, []*ical.Event{modifiedMaster})
}

func (h *Handlers) calendarExists(ctx context.Context, owner, uri string) bool {
	cal, err := h.store.GetCalendarByURI(ctx, uri)
	if err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", uri).Msg("failed to check if calendar exists")
		return false
	}
	return cal != nil && cal.OwnerUserID == owner
}

func (h *Handlers) HandleMkcol(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || calURI == "" || len(rest) != 0 {
		if o2, c2, ok := tryCalendarShorthand(r.URL.Path, h.basePath, pr.UserID); ok {
			owner, calURI, rest = o2, c2, nil
		} else {
			h.logger.Error().Str("path", r.URL.Path).Msg("MKCOL with invalid path")
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
	}

	if !common.SafeCollectionName(calURI) {
		h.logger.Error().Str("calendar", calURI).Msg("unsafe collection name in MKCOL")
		http.Error(w, "bad collection name", http.StatusBadRequest)
		return
	}

	if pr.UserID != owner {
		if !h.checkBindAccess(w, r.Context(), pr, "") {
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read MKCOL body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	displayName, description, color := h.parseMkcolRequest(body)

	if h.calendarExists(r.Context(), owner, calURI) {
		h.logger.Debug().Str("owner", owner).Str("calendar", calURI).Msg("calendar already exists in MKCOL")
		http.Error(w, "conflict", http.StatusConflict)
		return
	}

	newCal := storage.Calendar{
		OwnerUserID: owner,
		URI:         calURI,
		DisplayName: displayName,
		Description: description,
		Color:       color,
	}
	if err := h.store.CreateCalendar(newCal, "", description); err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to create calendar in MKCOL")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) HandleMkcalendar(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || calURI == "" || len(rest) != 0 {
		if o2, c2, ok := tryCalendarShorthand(r.URL.Path, h.basePath, pr.UserID); ok {
			owner, calURI, rest = o2, c2, nil
		} else {
			h.logger.Error().Str("path", r.URL.Path).Msg("MKCALENDAR with invalid path")
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
	}

	if pr.UserID != owner {
		h.logger.Debug().Str("user", pr.UserID).Str("owner", owner).Msg("insufficient privileges for MKCALENDAR")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if !common.SafeCollectionName(calURI) {
		h.logger.Error().Str("calendar", calURI).Msg("unsafe collection name in MKCALENDAR")
		http.Error(w, "bad collection name", http.StatusBadRequest)
		return
	}

	if h.calendarExists(r.Context(), owner, calURI) {
		h.logger.Debug().Str("owner", owner).Str("calendar", calURI).Msg("calendar already exists in MKCALENDAR")
		http.Error(w, "conflict", http.StatusConflict)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read MKCALENDAR body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	displayName, description, color := h.parseMkcalendarRequest(body)

	newCal := storage.Calendar{
		OwnerUserID: owner,
		URI:         calURI,
		DisplayName: displayName,
		Description: description,
		Color:       color,
	}
	if err := h.store.CreateCalendar(newCal, "", description); err != nil {
		h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to create calendar in MKCALENDAR")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) HandleProppatch(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || calURI == "" || len(rest) != 0 {
		h.logger.Error().Str("path", r.URL.Path).Msg("PROPPATCH with invalid path")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if !common.SafeSegment(calURI) {
		h.logger.Error().Str("calendar", calURI).Msg("unsafe path in PROPPATCH")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		if !h.checkWritePropsAccess(w, r.Context(), pr, calURI) {
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read PROPPATCH body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	newName, newColor, hasColorUpdate := h.parseProppatchRequest(body)

	displayNameStatus := http.StatusOK
	if newName != nil {
		if err := h.store.UpdateCalendarDisplayName(r.Context(), owner, calURI, newName); err != nil {
			h.logger.Error().Err(err).Msg("Failed to update calendar display name")
			displayNameStatus = http.StatusInternalServerError
		}
	}

	colorStatus := http.StatusOK
	if hasColorUpdate {
		if !common.IsValidHexColor(newColor) {
			colorStatus = http.StatusBadRequest
		} else {
			if err := h.store.UpdateCalendarColor(r.Context(), owner, calURI, newColor); err != nil {
				h.logger.Error().Err(err).Msg("Failed to update calendar color")
				colorStatus = http.StatusInternalServerError
			}
		}
	}

	h.writeProppatchResponse(w, r.URL.Path, newName, newColor, hasColorUpdate, displayNameStatus, colorStatus)
}

func (h *Handlers) HandleReport(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if owner != "" && calURI != "" && len(rest) == 0 {
		_, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
		if err != nil {
			h.logger.Error().Err(err).Str("owner", owner).Str("calendar", calURI).Msg("failed to resolve calendar in REPORT")
			http.NotFound(w, r)
			return
		}

		if pr.UserID != calOwner {
			if !h.checkReadAccess(w, r.Context(), pr, calURI) {
				return
			}
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read REPORT body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	root := struct {
		XMLName xml.Name
	}{}
	if err := xml.Unmarshal(body, &root); err != nil {
		h.logger.Error().Err(err).Msg("failed to unmarshal REPORT XML")
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}

	switch root.XMLName.Space + " " + root.XMLName.Local {
	case common.NSCalDAV + " calendar-query":
		var q common.CalendarQuery
		if err := xml.Unmarshal(body, &q); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal calendar-query")
		}
		h.ReportCalendarQuery(w, r, q)
	case common.NSCalDAV + " calendar-multiget":
		var mg common.CalendarMultiget
		if err := xml.Unmarshal(body, &mg); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal calendar-multiget")
		}
		h.ReportCalendarMultiget(w, r, mg)
	case common.NSDAV + " sync-collection":
		var sc common.SyncCollection
		if err := xml.Unmarshal(body, &sc); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal sync-collection")
		}
		h.ReportSyncCollection(w, r, sc)
	case common.NSCalDAV + " free-busy-query":
		var fb common.FreeBusyQuery
		if err := xml.Unmarshal(body, &fb); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal free-busy-query")
		}
		h.ReportFreeBusyQuery(w, r, fb)
	default:
		h.logger.Error().Str("namespace", root.XMLName.Space).Str("local", root.XMLName.Local).Msg("unsupported REPORT type")
		http.Error(w, "unsupported REPORT", http.StatusBadRequest)
	}
}

func (h *Handlers) storeEvents(ctx context.Context, calendarID string, events []*ical.Event) error {
	if len(events) == 0 {
		return nil
	}

	var master *storage.Object
	var exceptions []*storage.Object

	for _, event := range events {
		data, err := ical.SerializeEvent(event)
		if err != nil {
			return fmt.Errorf("failed to serialize event: %w", err)
		}

		data, _ = ical.EnsureDTStamp(data)

		component, err := ical.DetectICSComponent(data)
		if err != nil {
			h.logger.Warn().Err(err).Msg("failed to detect component type, defaulting to VEVENT")
			component = "VEVENT"
		}

		obj := &storage.Object{
			CalendarID: calendarID,
			UID:        event.UID,
			Data:       string(data),
			Component:  component,
			StartAt:    &event.Start,
			EndAt:      &event.End,
		}

		if event.RecurrenceID != nil {
			exceptions = append(exceptions, obj)
		} else {
			master = obj
		}
	}

	if master != nil && len(exceptions) > 0 {
		return h.store.PutEventWithExceptions(ctx, master, exceptions)
	} else if master != nil {
		return h.store.PutObject(ctx, master)
	} else if len(exceptions) > 0 {
		for _, exc := range exceptions {
			if err := h.store.PutObject(ctx, exc); err != nil {
				return err
			}
		}
	}

	return nil
}
