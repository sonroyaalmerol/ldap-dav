package caldav

import (
	"context"
	"database/sql"
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

	// Get the master event
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

	// Get exceptions if this is a recurring event
	exceptions, err := h.store.GetEventExceptions(r.Context(), calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Str("uid", uid).Msg("failed to get exceptions, serving master only")
		h.writeObjectResponse(w, obj)
		return
	}

	// Combine master + exceptions into a single iCalendar response
	if len(exceptions) > 0 {
		combinedData, err := h.combineEventWithExceptions(obj, exceptions)
		if err != nil {
			h.logger.Error().Err(err).Msg("failed to combine event with exceptions")
			h.writeObjectResponse(w, obj)
			return
		}

		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("ETag", `"`+obj.ETag+`"`)
		if !obj.UpdatedAt.IsZero() {
			w.Header().Set("Last-Modified", obj.UpdatedAt.UTC().Format(time.RFC1123))
		}
		_, _ = w.Write([]byte(combinedData))
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

	existing, _ := h.store.GetObject(r.Context(), calendarID, uid)

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

	component, err := ical.DetectICSComponent(raw)
	if err != nil {
		h.logger.Error().Err(err).Msg("unsupported calendar component in PUT")
		http.Error(w, "unsupported calendar component", http.StatusUnsupportedMediaType)
		return
	}

	// Parse calendar data - may contain master + exceptions
	events, err := ical.ParseCalendar(raw)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to parse incoming calendar")
		http.Error(w, "invalid ical", http.StatusBadRequest)
		return
	}

	// Handle non-VEVENT components (VTODO, VJOURNAL, etc.)
	if len(events) == 0 {
		if component == "VEVENT" {
			h.logger.Error().Msg("no events found in VEVENT calendar")
			http.Error(w, "no events", http.StatusBadRequest)
			return
		}

		h.logger.Debug().Str("component", component).Msg("storing non-VEVENT component")

		// Validate ETags
		if !h.validateETags(r, existing, nil) {
			http.Error(w, "precondition failed", http.StatusPreconditionFailed)
			return
		}

		// Store as raw object
		obj := &storage.Object{
			CalendarID: calendarID,
			UID:        uid,
			Data:       string(raw),
			Component:  component,
		}

		if err := h.store.PutObject(r.Context(), obj); err != nil {
			h.logger.Error().Err(err).Msg("failed to store non-event object")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}

		storedObj, err := h.store.GetObject(r.Context(), calendarID, uid)
		if err != nil {
			h.logger.Warn().Err(err).Msg("stored but failed to retrieve object")
			if existing == nil {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}

		w.Header().Set("ETag", `"`+storedObj.ETag+`"`)
		if existing == nil {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	// Separate master and exceptions
	var master *ical.Event
	var exceptions []*ical.Event

	removeMe := true
	for i, event := range events {
		if removeMe {
			h.logger.Debug().
				Int("index", i).
				Str("uid", event.UID).
				Str("expected_uid", uid).
				Interface("recurrence_id", event.RecurrenceID).
				Msg("parsed event")
			continue
		}
		if event.UID != "" && event.UID != uid {
			h.logger.Error().
				Str("expected_uid", uid).
				Str("event_uid", event.UID).
				Msg("event UID mismatch")
			http.Error(w, "UID mismatch", http.StatusBadRequest)
			return
		}
		event.UID = uid // Ensure UID matches URL

		if event.RecurrenceID != nil {
			exceptions = append(exceptions, event)
		} else {
			if master != nil {
				h.logger.Error().Msg("multiple master events in single PUT")
				http.Error(w, "multiple master events", http.StatusBadRequest)
				return
			}
			master = event
		}
	}
	if removeMe {
		return
	}

	// Handle case: Client sends only exception (modifying single instance)
	if master == nil && len(exceptions) == 1 {
		if err := h.handleExceptionOnlyPut(w, r, calendarID, uid, exceptions[0], existing); err != nil {
			h.logger.Error().Err(err).Msg("failed to handle exception-only PUT")
			http.Error(w, "failed to store exception", http.StatusInternalServerError)
		}
		return
	}

	// Handle case: Client sends only exception(s) without master - need to check if master exists
	if master == nil && len(exceptions) > 0 {
		if existing == nil {
			h.logger.Error().Msg("cannot create exceptions without master event")
			http.Error(w, "master event required", http.StatusBadRequest)
			return
		}

		// Store exceptions only
		for _, exception := range exceptions {
			if err := h.storeException(r.Context(), calendarID, uid, exception); err != nil {
				h.logger.Error().Err(err).Msg("failed to store exception")
				http.Error(w, "storage error", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("ETag", `"`+existing.ETag+`"`)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Validate ETags
	if !h.validateETags(r, existing, nil) {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}

	// Store master + exceptions
	if err := h.storeEvents(r.Context(), calendarID, events); err != nil {
		h.logger.Error().Err(err).Msg("failed to store events")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	// Return response
	storedObj, err := h.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Msg("stored but failed to retrieve object")
		if existing == nil {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	w.Header().Set("ETag", `"`+storedObj.ETag+`"`)
	if existing == nil {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
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

	// Check if client is deleting specific instance via query parameter or X-CALDAV-RECURRENCE-ID header
	var recurrenceID *time.Time

	// Try query parameter first: ?recurrence-id=20250115T100000Z
	if recIDStr := r.URL.Query().Get("recurrence-id"); recIDStr != "" {
		t, err := common.ParseICalTime(recIDStr)
		if err != nil {
			h.logger.Error().Err(err).Str("recurrence-id", recIDStr).Msg("invalid recurrence-id in query")
			http.Error(w, "invalid recurrence-id", http.StatusBadRequest)
			return
		}
		recurrenceID = &t
	}

	// Try custom header: X-CALDAV-RECURRENCE-ID: 20250115T100000Z
	if recurrenceID == nil {
		if recIDStr := r.Header.Get("X-CALDAV-RECURRENCE-ID"); recIDStr != "" {
			t, err := common.ParseICalTime(recIDStr)
			if err != nil {
				h.logger.Error().Err(err).Str("recurrence-id", recIDStr).Msg("invalid recurrence-id in header")
				http.Error(w, "invalid recurrence-id", http.StatusBadRequest)
				return
			}
			recurrenceID = &t
		}
	}

	// Get the existing object (master event)
	existing, err := h.store.GetObject(r.Context(), calendarID, uid)
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

	if recurrenceID != nil {
		// Delete single instance by adding EXDATE or deleting exception
		if err := h.deleteSingleInstance(r.Context(), calendarID, uid, *recurrenceID); err != nil {
			if err == sql.ErrNoRows {
				http.NotFound(w, r)
				return
			}
			h.logger.Error().Err(err).Str("uid", uid).Msg("failed to delete instance")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
	} else {
		// Delete entire event (master + all exceptions)
		if err := h.deleteEntireEvent(r.Context(), calendarID, uid); err != nil {
			if err == sql.ErrNoRows {
				http.NotFound(w, r)
				return
			}
			h.logger.Error().Err(err).Str("uid", uid).Msg("failed to delete event")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// deleteSingleInstance handles deletion of a single recurring instance
func (h *Handlers) deleteSingleInstance(ctx context.Context, calendarID, uid string, recurrenceID time.Time) error {
	exceptions, err := h.store.GetEventExceptions(ctx, calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Msg("failed to get exceptions")
	}

	exceptionExists := false
	for _, exc := range exceptions {
		excEvents, err := ical.ParseCalendar([]byte(exc.Data))
		if err != nil {
			continue
		}
		for _, excEvent := range excEvents {
			if excEvent.RecurrenceID != nil && excEvent.RecurrenceID.Equal(recurrenceID) {
				exceptionExists = true
				// Delete the exception object
				if err := h.store.DeleteObject(ctx, calendarID, exc.UID, ""); err != nil {
					return fmt.Errorf("failed to delete exception: %w", err)
				}
				break
			}
		}
		if exceptionExists {
			break
		}
	}

	master, err := h.store.GetObject(ctx, calendarID, uid)
	if err != nil {
		return fmt.Errorf("failed to get master event: %w", err)
	}

	events, err := ical.ParseCalendar([]byte(master.Data))
	if err != nil {
		return fmt.Errorf("failed to parse master event: %w", err)
	}

	if len(events) == 0 {
		return fmt.Errorf("no events found in master")
	}

	masterEvent := events[0]

	if !masterEvent.IsRecurring {
		// Non-recurring event, can't delete single instance
		return fmt.Errorf("cannot delete instance of non-recurring event")
	}

	ical.AddExceptionDate(masterEvent, recurrenceID)

	updatedData, err := ical.SerializeEvent(masterEvent)
	if err != nil {
		return fmt.Errorf("failed to serialize updated event: %w", err)
	}

	master.Data = string(updatedData)
	master.StartAt = &masterEvent.Start
	master.EndAt = &masterEvent.End

	if err := h.store.PutObject(ctx, master); err != nil {
		return fmt.Errorf("failed to update master event: %w", err)
	}

	h.logger.Debug().
		Str("uid", uid).
		Time("recurrence_id", recurrenceID).
		Bool("had_exception", exceptionExists).
		Msg("deleted single instance via EXDATE")

	return nil
}

// deleteEntireEvent deletes the master event and all exceptions
func (h *Handlers) deleteEntireEvent(ctx context.Context, calendarID, uid string) error {
	// Step 1: Get all exceptions
	exceptions, err := h.store.GetEventExceptions(ctx, calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).Str("uid", uid).Msg("failed to get exceptions before delete")
	}

	// Step 2: Delete all exceptions first
	for _, exc := range exceptions {
		if err := h.store.DeleteObject(ctx, calendarID, exc.UID, ""); err != nil {
			h.logger.Warn().Err(err).Str("exception_uid", exc.UID).Msg("failed to delete exception")
		}
	}

	// Step 3: Delete master event
	return h.store.DeleteObject(ctx, calendarID, uid, "")
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

	for _, event := range events {
		data, err := ical.SerializeEvent(event)
		if err != nil {
			return fmt.Errorf("failed to serialize event: %w", err)
		}

		data, _ = ical.EnsureDTStamp(data)

		component, err := ical.DetectICSComponent(data)
		if err != nil {
			h.logger.Warn().Err(err).Msg("failed to detect component type")
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

		// PutObject will handle master vs exception based on RECURRENCE-ID
		if err := h.store.PutObject(ctx, obj); err != nil {
			return err
		}
	}

	return nil
}
