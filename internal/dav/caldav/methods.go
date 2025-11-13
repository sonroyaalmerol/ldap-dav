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

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
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
		h.logger.Error().
			Str("calendar", calURI).
			Str("uid", uid).
			Msg("GET request with unsafe path segments")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to resolve calendar in GET")
		http.NotFound(w, r)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if pr.UserID != calOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("ACL check failed in GET")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.Read {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("insufficient DAV:read privileges for GET")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// Check if this is a recurring instance request
	if h.isRecurringInstanceRequest(r.URL.Path) {
		h.handleGetRecurringInstance(w, r, calendarID)
		return
	}

	obj, err := h.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("failed to get object in GET")
		http.NotFound(w, r)
		return
	}

	inm := common.TrimQuotes(r.Header.Get("If-None-Match"))
	if inm != "" && inm == obj.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if !obj.UpdatedAt.IsZero() {
		w.Header().Set("Last-Modified", obj.UpdatedAt.UTC().Format(time.RFC1123))
	}
	_, _ = io.WriteString(w, obj.Data)
}

func (h *Handlers) handleGetRecurringInstance(w http.ResponseWriter, r *http.Request, calendarID string) {
	baseUID := h.extractBaseUIDFromHref(r.URL.Path)
	recurrenceTime, err := h.parseRecurrenceIDFromHref(r.URL.Path)
	if err != nil || recurrenceTime == nil {
		h.logger.Error().Err(err).
			Str("path", r.URL.Path).
			Msg("failed to parse recurrence-id from path")
		http.NotFound(w, r)
		return
	}

	// Use the helper to get complete event
	masterObj, exceptions, err := h.getCompleteRecurringEvent(r.Context(), calendarID, baseUID)
	if err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("baseUID", baseUID).
			Msg("failed to get master object for recurring instance")
		http.NotFound(w, r)
		return
	}

	// First, check if there's an explicit exception for this recurrence
	for _, exc := range exceptions {
		excEvents, err := ical.ParseCalendar([]byte(exc.Data))
		if err == nil && len(excEvents) > 0 {
			if excEvents[0].RecurrenceID != nil && excEvents[0].RecurrenceID.Equal(*recurrenceTime) {
				// Found explicit exception, return it
				h.logger.Debug().
					Str("uid", baseUID).
					Time("recurrence_id", *recurrenceTime).
					Msg("returning explicit exception instance")

				inm := common.TrimQuotes(r.Header.Get("If-None-Match"))
				if inm != "" && inm == exc.ETag {
					w.WriteHeader(http.StatusNotModified)
					return
				}

				w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
				w.Header().Set("ETag", `"`+exc.ETag+`"`)
				if !exc.UpdatedAt.IsZero() {
					w.Header().Set("Last-Modified", exc.UpdatedAt.UTC().Format(time.RFC1123))
				}
				_, _ = io.WriteString(w, exc.Data)
				return
			}
		}
	}

	// No explicit exception, expand from master
	events, err := ical.ParseCalendar([]byte(masterObj.Data))
	if err != nil {
		h.logger.Error().Err(err).Str("uid", baseUID).Msg("failed to parse calendar")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasRecurrence := false
	for _, event := range events {
		if event.IsRecurring {
			hasRecurrence = true
			break
		}
	}

	if !hasRecurrence {
		http.NotFound(w, r)
		return
	}

	start := recurrenceTime.Add(-24 * time.Hour)
	end := recurrenceTime.Add(24 * time.Hour)

	expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to expand recurrences")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var targetEvent *ical.Event
	for _, event := range expandedEvents {
		if event.RecurrenceID != nil && event.RecurrenceID.Equal(*recurrenceTime) {
			targetEvent = event
			break
		}
	}

	if targetEvent == nil {
		http.NotFound(w, r)
		return
	}

	instanceETag := h.generateInstanceETag(masterObj.ETag, targetEvent)

	inm := common.TrimQuotes(r.Header.Get("If-None-Match"))
	if inm != "" && inm == instanceETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	instanceData, err := ical.SerializeEvent(targetEvent)
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
		h.logger.Error().
			Str("calendar", calURI).
			Str("uid", uid).
			Msg("PUT request with unsafe path segments")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to resolve calendar in PUT")
		http.NotFound(w, r)
		return
	}

	pr := common.MustPrincipal(r.Context())

	// Check if this is a recurring instance
	isRecurringInstance := h.isRecurringInstanceRequest(r.URL.Path)
	lookupUID := uid
	var recurrenceID *time.Time

	if isRecurringInstance {
		lookupUID = h.extractBaseUIDFromHref(r.URL.Path)
		recID, err := h.parseRecurrenceIDFromHref(r.URL.Path)
		if err == nil && recID != nil {
			recurrenceID = recID
		}
	}

	existing, _ := h.store.GetObject(r.Context(), calendarID, lookupUID)

	if pr.UserID != calOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("ACL check failed in PUT")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if existing == nil {
			if !eff.Bind {
				h.logger.Debug().
					Str("user", pr.UserID).
					Str("calendar", calURI).
					Msg("insufficient DAV:bind privileges for creating new resource")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		} else {
			if !eff.WriteContent {
				h.logger.Debug().
					Str("user", pr.UserID).
					Str("calendar", calURI).
					Msg("insufficient DAV:write-content privileges for modifying existing resource")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
	}

	maxICS := h.cfg.HTTP.MaxICSBytes
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxICS+1))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read PUT body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()
	if len(raw) == 0 {
		h.logger.Error().Msg("empty body in PUT request")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	if maxICS > 0 && int64(len(raw)) > maxICS {
		h.logger.Error().
			Int("size", len(raw)).
			Int64("max", maxICS).
			Msg("payload too large in PUT")
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	if fixed, inserted := ical.EnsureDTStamp(raw); inserted {
		raw = fixed
	}

	ics, err := ical.NormalizeICS(raw)
	if err != nil {
		h.logger.Error().Err(err).Bytes("raw_ics", raw).Msg("normalize ics failed")
		http.Error(w, "invalid ical", http.StatusBadRequest)
		return
	}

	_, err = ical.DetectICSComponent(ics)
	if err != nil {
		h.logger.Error().Err(err).Msg("unsupported calendar component in PUT")
		http.Error(w, "unsupported calendar component", http.StatusUnsupportedMediaType)
		return
	}

	events, err := ical.ParseCalendar(ics)
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
	incomingEvent.UID = lookupUID // Ensure UID matches

	// ETag validation
	wantNew := r.Header.Get("If-None-Match") == "*"
	match := common.TrimQuotes(r.Header.Get("If-Match"))

	if wantNew && existing != nil {
		h.logger.Debug().Str("uid", lookupUID).Msg("precondition failed - object exists")
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}

	if match != "" && existing != nil {
		if isRecurringInstance && recurrenceID != nil {
			// Validate instance ETag
			existingEvents, err := ical.ParseCalendar([]byte(existing.Data))
			if err == nil {
				start := recurrenceID.Add(-24 * time.Hour)
				end := recurrenceID.Add(24 * time.Hour)
				expandedEvents, err := h.expander.ExpandRecurrences(existingEvents, start, end)
				if err == nil {
					found := false
					for _, event := range expandedEvents {
						if event.RecurrenceID != nil && event.RecurrenceID.Equal(*recurrenceID) {
							instanceETag := h.generateInstanceETag(existing.ETag, event)
							if instanceETag != match {
								h.logger.Debug().
									Str("uid", uid).
									Str("expected_etag", match).
									Str("actual_etag", instanceETag).
									Msg("precondition failed - instance etag mismatch")
								http.Error(w, "precondition failed", http.StatusPreconditionFailed)
								return
							}
							found = true
							break
						}
					}
					if !found && match != existing.ETag {
						h.logger.Debug().
							Str("uid", lookupUID).
							Str("expected_etag", match).
							Msg("precondition failed - instance not found and master etag mismatch")
						http.Error(w, "precondition failed", http.StatusPreconditionFailed)
						return
					}
				}
			}
		} else {
			// Regular ETag validation
			if existing.ETag != match {
				h.logger.Debug().
					Str("uid", lookupUID).
					Str("expected_etag", match).
					Str("actual_etag", existing.ETag).
					Msg("precondition failed - etag mismatch")
				http.Error(w, "precondition failed", http.StatusPreconditionFailed)
				return
			}
		}
	}

	// Determine modification type
	modType := ical.ModifyAll

	if isRecurringInstance || incomingEvent.RecurrenceID != nil {
		modType = ical.ModifyThis
		if recurrenceID == nil {
			recurrenceID = incomingEvent.RecurrenceID
		}
	}

	// Check for "this and future" based on custom header or parameter
	if r.Header.Get("X-Modify-Future") == "true" || r.URL.Query().Get("this-and-future") == "true" {
		if recurrenceID != nil {
			modType = ical.ModifyThisFuture
		}
	}

	// If we're modifying a single instance or "this and future" of a recurring event,
	// we need to check for existing exceptions
	if existing != nil && (modType == ical.ModifyThis || modType == ical.ModifyThisFuture) {
		// Check if this is actually a recurring event
		existingEvents, err := ical.ParseCalendar([]byte(existing.Data))
		if err == nil && len(existingEvents) > 0 && existingEvents[0].IsRecurring {
			// Get existing exceptions to preserve them
			existingExceptions, err := h.store.GetEventExceptions(r.Context(), calendarID, lookupUID)
			if err != nil {
				h.logger.Warn().Err(err).
					Str("uid", lookupUID).
					Msg("failed to get existing exceptions, continuing without them")
			} else if len(existingExceptions) > 0 {
				h.logger.Debug().
					Str("uid", lookupUID).
					Int("exception_count", len(existingExceptions)).
					Msg("found existing exceptions for recurring event")
				// The exceptions will be preserved when we store
			}
		}
	}

	// Prepare the event for storage
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

	// Validate that we got events to store
	if len(eventsToStore) == 0 {
		h.logger.Error().Msg("no events to store after preparation")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Store the events
	if err := h.storeEvents(r.Context(), calendarID, eventsToStore); err != nil {
		h.logger.Error().Err(err).Msg("failed to store events")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	// Get the stored object to return its ETag
	storedObj, err := h.store.GetObject(r.Context(), calendarID, lookupUID)
	if err != nil {
		h.logger.Warn().Err(err).Msg("stored but failed to retrieve object")
		// Still return success since we stored it
		if existing == nil {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	responseETag := storedObj.ETag
	if isRecurringInstance && recurrenceID != nil {
		responseETag = h.generateInstanceETag(storedObj.ETag, &ical.Event{RecurrenceID: recurrenceID})
	}

	w.Header().Set("ETag", `"`+responseETag+`"`)
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
		h.logger.Error().
			Str("path", r.URL.Path).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("DELETE request with invalid path")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if len(rest) == 0 {
		if !common.SafeCollectionName(calURI) {
			h.logger.Error().Str("calendar", calURI).Msg("unsafe collection name in DELETE")
			http.Error(w, "bad collection name", http.StatusBadRequest)
			return
		}

		if pr.UserID != owner {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("insufficient privileges for DELETE calendar")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := h.store.DeleteCalendar(owner, calURI); err != nil {
			h.logger.Error().Err(err).
				Str("owner", owner).
				Str("calendar", calURI).
				Msg("failed to delete calendar")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		h.logger.Error().
			Str("calendar", calURI).
			Str("uid", uid).
			Msg("unsafe path segments in DELETE object")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to resolve calendar in DELETE")
		http.NotFound(w, r)
		return
	}

	if pr.UserID != calOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("ACL check failed in DELETE object")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.Unbind {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("insufficient DAV:unbind privileges for DELETE object")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	// Check if this is a recurring instance
	isRecurringInstance := h.isRecurringInstanceRequest(r.URL.Path)
	lookupUID := uid
	var recurrenceID *time.Time

	if isRecurringInstance {
		lookupUID = h.extractBaseUIDFromHref(r.URL.Path)
		recID, err := h.parseRecurrenceIDFromHref(r.URL.Path)
		if err == nil && recID != nil {
			recurrenceID = recID
		}
	}

	match := common.TrimQuotes(r.Header.Get("If-Match"))

	// Get the existing object
	existing, err := h.store.GetObject(r.Context(), calendarID, lookupUID)
	if err != nil {
		h.logger.Error().Err(err).Msg("object not found")
		http.NotFound(w, r)
		return
	}

	// Validate ETag if provided
	if match != "" {
		if isRecurringInstance && recurrenceID != nil {
			// Validate instance ETag
			events, err := ical.ParseCalendar([]byte(existing.Data))
			if err == nil {
				start := recurrenceID.Add(-24 * time.Hour)
				end := recurrenceID.Add(24 * time.Hour)
				expandedEvents, err := h.expander.ExpandRecurrences(events, start, end)
				if err == nil {
					found := false
					for _, event := range expandedEvents {
						if event.RecurrenceID != nil && event.RecurrenceID.Equal(*recurrenceID) {
							instanceETag := h.generateInstanceETag(existing.ETag, event)
							if instanceETag != match {
								h.logger.Debug().
									Str("uid", uid).
									Str("expected_etag", match).
									Str("actual_etag", instanceETag).
									Msg("precondition failed - instance etag mismatch in DELETE")
								http.Error(w, "precondition failed", http.StatusPreconditionFailed)
								return
							}
							found = true
							break
						}
					}
					if !found {
						http.NotFound(w, r)
						return
					}
				}
			}
		} else {
			// Regular ETag validation
			if existing.ETag != match {
				h.logger.Debug().
					Str("uid", lookupUID).
					Str("expected_etag", match).
					Str("actual_etag", existing.ETag).
					Msg("precondition failed - etag mismatch in DELETE")
				http.Error(w, "precondition failed", http.StatusPreconditionFailed)
				return
			}
		}
	}

	// Determine modification type
	modType := ical.ModifyAll
	if recurrenceID != nil {
		modType = ical.ModifyThis
	}

	// Check for "this and future"
	if r.URL.Query().Get("this-and-future") == "true" || r.Header.Get("X-Modify-Future") == "true" {
		if recurrenceID != nil {
			modType = ical.ModifyThisFuture
		}
	}

	// Handle the delete based on modification type
	if modType == ical.ModifyAll && recurrenceID == nil {
		// Simple delete - remove master AND all exceptions

		// First, try to get and delete all exceptions
		exceptions, err := h.store.GetEventExceptions(r.Context(), calendarID, lookupUID)
		if err != nil {
			h.logger.Warn().Err(err).
				Str("uid", lookupUID).
				Msg("failed to get exceptions before delete, will attempt to delete master anyway")
		}

		// Delete all exceptions first
		if len(exceptions) > 0 {
			h.logger.Debug().
				Str("uid", lookupUID).
				Int("exception_count", len(exceptions)).
				Msg("deleting event exceptions before master")

			for _, exc := range exceptions {
				// Each exception has the same UID but different RECURRENCE-ID
				if err := h.store.DeleteObject(r.Context(), calendarID, exc.UID, ""); err != nil {
					h.logger.Warn().Err(err).
						Str("uid", exc.UID).
						Msg("failed to delete exception, continuing")
				}
			}
		}

		// Now delete the master event
		if err := h.store.DeleteEventInstance(r.Context(), calendarID, lookupUID, nil); err != nil {
			h.logger.Error().Err(err).Msg("failed to delete event")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
	} else {
		// Complex delete - need to modify master event
		events, err := ical.ParseCalendar([]byte(existing.Data))
		if err != nil {
			h.logger.Error().Err(err).Msg("failed to parse existing event")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if len(events) == 0 {
			h.logger.Error().Msg("no events in calendar data")
			http.NotFound(w, r)
			return
		}

		deleteReq := &ical.EventDeleteRequest{
			UID:              lookupUID,
			ModificationType: modType,
			RecurrenceID:     recurrenceID,
		}

		modifiedMaster, err := h.recurrenceManager.PrepareEventDelete(deleteReq, events[0])
		if err != nil {
			h.logger.Error().Err(err).Msg("failed to prepare delete")
			http.Error(w, fmt.Sprintf("failed to prepare delete: %v", err), http.StatusBadRequest)
			return
		}

		if modifiedMaster == nil {
			// Delete entire series
			if err := h.store.DeleteEventInstance(r.Context(), calendarID, lookupUID, nil); err != nil {
				h.logger.Error().Err(err).Msg("failed to delete event series")
				http.Error(w, "storage error", http.StatusInternalServerError)
				return
			}
		} else {
			// Update master with EXDATE or truncated RRULE
			if err := h.storeEvents(r.Context(), calendarID, []*ical.Event{modifiedMaster}); err != nil {
				h.logger.Error().Err(err).Msg("failed to update master event")
				http.Error(w, "storage error", http.StatusInternalServerError)
				return
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) calendarExists(ctx context.Context, owner, uri string) bool {
	cal, err := h.store.GetCalendarByURI(ctx, uri)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", uri).
			Msg("failed to check if calendar exists")
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
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, "")
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("owner", owner).
				Msg("ACL check failed in MKCOL")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.Bind {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("owner", owner).
				Msg("insufficient DAV:bind privileges for MKCOL")
			http.Error(w, "forbidden", http.StatusForbidden)
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

	type mkcolProp struct {
		XMLName      xml.Name `xml:"DAV: prop"`
		DisplayName  *string  `xml:"DAV: displayname"`
		Description  *string  `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
		ResourceType struct {
			Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
		} `xml:"DAV: resourcetype"`
		Raw []common.RawXMLValue `xml:",any"`
	}
	var mkcolReq struct {
		XMLName xml.Name `xml:"DAV: mkcol"`
		Set     *struct {
			XMLName xml.Name  `xml:"DAV: set"`
			Prop    mkcolProp `xml:"DAV: prop"`
		} `xml:"DAV: set"`
	}

	if len(body) > 0 {
		if err := xml.Unmarshal(body, &mkcolReq); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal MKCOL XML")
		}
	}

	isCalendar := mkcolReq.Set != nil && mkcolReq.Set.Prop.ResourceType.Calendar != nil
	if !isCalendar {
		h.logger.Error().Msg("MKCOL with unsupported collection type")
		http.Error(w, "unsupported collection type", http.StatusUnsupportedMediaType)
		return
	}

	if h.calendarExists(r.Context(), owner, calURI) {
		h.logger.Debug().
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("calendar already exists in MKCOL")
		http.Error(w, "conflict", http.StatusConflict)
		return
	}

	var displayName string
	var description string
	var color string

	if mkcolReq.Set != nil {
		if mkcolReq.Set.Prop.DisplayName != nil {
			displayName = *mkcolReq.Set.Prop.DisplayName
		}
		if mkcolReq.Set.Prop.Description != nil {
			description = *mkcolReq.Set.Prop.Description
		}

		for _, rawProp := range mkcolReq.Set.Prop.Raw {
			var colorProp struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
				Text    string   `xml:",chardata"`
			}

			xmlBytes, err := xml.Marshal(&rawProp)
			if err != nil {
				continue
			}

			if err := xml.Unmarshal(xmlBytes, &colorProp); err == nil {
				if colorProp.XMLName.Space == "http://apple.com/ns/ical/" &&
					colorProp.XMLName.Local == "calendar-color" {
					color = colorProp.Text
					break
				}
			}
		}
	}

	if color != "" && !common.IsValidHexColor(color) {
		color = "#3174ad"
	}

	newCal := storage.Calendar{
		OwnerUserID: owner,
		URI:         calURI,
		DisplayName: displayName,
		Description: description,
		Color:       color,
	}
	if err := h.store.CreateCalendar(newCal, "", description); err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to create calendar in MKCOL")
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
		h.logger.Debug().
			Str("user", pr.UserID).
			Str("owner", owner).
			Msg("insufficient privileges for MKCALENDAR")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if !common.SafeCollectionName(calURI) {
		h.logger.Error().Str("calendar", calURI).Msg("unsafe collection name in MKCALENDAR")
		http.Error(w, "bad collection name", http.StatusBadRequest)
		return
	}

	if h.calendarExists(r.Context(), owner, calURI) {
		h.logger.Debug().
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("calendar already exists in MKCALENDAR")
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

	type mkcalProp struct {
		XMLName             xml.Name             `xml:"DAV: prop"`
		DisplayName         *string              `xml:"DAV: displayname"`
		CalendarDescription *string              `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
		Raw                 []common.RawXMLValue `xml:",any"`
	}
	var mkcalReq struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav mkcalendar"`
		Set     *struct {
			XMLName xml.Name  `xml:"DAV: set"`
			Prop    mkcalProp `xml:"DAV: prop"`
		} `xml:"DAV: set"`
	}

	var displayName string
	var description string
	var color string

	if len(body) > 0 {
		if err := xml.Unmarshal(body, &mkcalReq); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal MKCALENDAR XML")
		} else {
			if mkcalReq.Set != nil {
				if mkcalReq.Set.Prop.DisplayName != nil {
					displayName = *mkcalReq.Set.Prop.DisplayName
				}
				if mkcalReq.Set.Prop.CalendarDescription != nil {
					description = *mkcalReq.Set.Prop.CalendarDescription
				}

				for _, rawProp := range mkcalReq.Set.Prop.Raw {
					var colorProp struct {
						XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
						Text    string   `xml:",chardata"`
					}

					xmlBytes, err := xml.Marshal(&rawProp)
					if err != nil {
						continue
					}

					if err := xml.Unmarshal(xmlBytes, &colorProp); err == nil {
						if colorProp.XMLName.Space == "http://apple.com/ns/ical/" &&
							colorProp.XMLName.Local == "calendar-color" {
							color = colorProp.Text
							if len(colorProp.Text) == 9 && colorProp.Text[0] == '#' { // #RRGGBBAA
								color = colorProp.Text[:7] // Keep only #RRGGBB
							}
							break
						}
					}
				}
			}
		}
	}

	if color != "" && !common.IsValidHexColor(color) {
		color = "#3174ad"
	}

	newCal := storage.Calendar{
		OwnerUserID: owner,
		URI:         calURI,
		DisplayName: displayName,
		Description: description,
		Color:       color,
	}
	if err := h.store.CreateCalendar(newCal, "", description); err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", calURI).
			Msg("failed to create calendar in MKCALENDAR")
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
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("ACL check failed in PROPPATCH")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.WriteProps {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("calendar", calURI).
				Msg("insufficient DAV:write-properties privileges for PROPPATCH")
			http.Error(w, "forbidden", http.StatusForbidden)
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

	type setRemoveProp struct {
		DisplayName *string              `xml:"DAV: displayname"`
		Raw         []common.RawXMLValue `xml:",any"`
	}
	type setRemove struct {
		XMLName xml.Name
		Prop    setRemoveProp `xml:"DAV: prop"`
	}
	var req struct {
		XMLName xml.Name   `xml:"DAV: propertyupdate"`
		Set     *setRemove `xml:"DAV: set"`
		Remove  *setRemove `xml:"DAV: remove"`
	}

	okXML := true
	if err := xml.Unmarshal(body, &req); err != nil {
		h.logger.Error().Err(err).Msg("failed to unmarshal PROPPATCH XML")
		okXML = false
	}

	var newName *string
	var newColor string
	hasColorUpdate := false
	var colorStatus int = http.StatusOK

	extractColorFromRaw := func(raw []common.RawXMLValue) string {
		for _, rawProp := range raw {
			var colorProp struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
				Text    string   `xml:",chardata"`
			}

			xmlBytes, err := xml.Marshal(&rawProp)
			if err != nil {
				continue
			}

			if err := xml.Unmarshal(xmlBytes, &colorProp); err == nil {
				if colorProp.XMLName.Space == "http://apple.com/ns/ical/" &&
					colorProp.XMLName.Local == "calendar-color" {
					newColor := colorProp.Text
					if len(colorProp.Text) == 9 && colorProp.Text[0] == '#' { // #RRGGBBAA
						newColor = colorProp.Text[:7] // Keep only #RRGGBB
					}
					return newColor
				}
			}
		}
		return ""
	}

	if okXML && req.Set != nil {
		if req.Set.Prop.DisplayName != nil {
			newName = req.Set.Prop.DisplayName
		}

		if color := extractColorFromRaw(req.Set.Prop.Raw); color != "" {
			newColor = color
			hasColorUpdate = true
		}
	}

	if okXML && req.Remove != nil {
		if req.Remove.Prop.DisplayName != nil {
			newName = nil
		}

		for _, rawProp := range req.Remove.Prop.Raw {
			xmlBytes, err := xml.Marshal(&rawProp)
			if err != nil {
				continue
			}

			var colorProp struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
			}

			if err := xml.Unmarshal(xmlBytes, &colorProp); err == nil {
				if colorProp.XMLName.Space == "http://apple.com/ns/ical/" &&
					colorProp.XMLName.Local == "calendar-color" {
					newColor = "#3174ad"
					hasColorUpdate = true
					break
				}
			}
		}
	}

	var displayNameStatus int = http.StatusOK

	if newName != nil || (okXML && req.Remove != nil && req.Remove.Prop.DisplayName != nil) {
		if err := h.store.UpdateCalendarDisplayName(r.Context(), owner, calURI, newName); err != nil {
			h.logger.Error().Err(err).Msg("Failed to update calendar display name")
			displayNameStatus = http.StatusInternalServerError
		}
	}

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

	resp := common.Response{
		Hrefs: []common.Href{{Value: r.URL.Path}},
	}

	if newName != nil || (okXML && req.Remove != nil && req.Remove.Prop.DisplayName != nil) {
		propValue := ""
		if newName != nil {
			propValue = *newName
		}
		if err := resp.EncodeProp(displayNameStatus, common.DisplayName{Name: propValue}); err != nil {
			h.logger.Error().Err(err).Msg("failed to encode DisplayName property in PROPPATCH")
		}
	}

	if hasColorUpdate {
		if colorStatus == http.StatusOK {
			if err := resp.EncodeProp(colorStatus, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
				Text    string   `xml:",chardata"`
			}{Text: newColor}); err != nil {
				h.logger.Error().Err(err).Msg("failed to encode calendar-color property in PROPPATCH")
			}
		} else {
			if err := resp.EncodeProp(colorStatus, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
			}{}); err != nil {
				h.logger.Error().Err(err).Msg("failed to encode calendar-color error in PROPPATCH")
			}
		}
	}

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPPATCH")
	}
}

func (h *Handlers) HandleReport(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if owner != "" && calURI != "" && len(rest) == 0 {
		_, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("owner", owner).
				Str("calendar", calURI).
				Msg("failed to resolve calendar in REPORT")
			http.NotFound(w, r)
			return
		}

		if pr.UserID != calOwner {
			eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
			if err != nil {
				h.logger.Error().Err(err).
					Str("user", pr.UserID).
					Str("calendar", calURI).
					Msg("ACL check failed in REPORT")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if !eff.Read {
				h.logger.Debug().
					Str("user", pr.UserID).
					Str("calendar", calURI).
					Msg("insufficient DAV:read privileges for REPORT")
				http.Error(w, "forbidden", http.StatusForbidden)
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
		h.logger.Error().
			Str("namespace", root.XMLName.Space).
			Str("local", root.XMLName.Local).
			Msg("unsupported REPORT type")
		http.Error(w, "unsupported REPORT", http.StatusBadRequest)
	}
}

func (h *Handlers) storeEvents(ctx context.Context, calendarID string, events []*ical.Event) error {
	if len(events) == 0 {
		return nil
	}

	// Separate master and exceptions
	var master *storage.Object
	var exceptions []*storage.Object

	for _, event := range events {
		data, err := ical.SerializeEvent(event)
		if err != nil {
			return fmt.Errorf("failed to serialize event: %w", err)
		}

		// Ensure data has DTSTAMP
		data, _ = ical.EnsureDTStamp(data)

		// Detect component type from the serialized data
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

	// Store using appropriate method
	if master != nil && len(exceptions) > 0 {
		return h.store.PutEventWithExceptions(ctx, master, exceptions)
	} else if master != nil {
		return h.store.PutObject(ctx, master)
	} else if len(exceptions) > 0 {
		// Store exceptions individually
		for _, exc := range exceptions {
			if err := h.store.PutObject(ctx, exc); err != nil {
				return err
			}
		}
	}

	return nil
}

func (h *Handlers) getCompleteRecurringEvent(ctx context.Context, calendarID, uid string) (*storage.Object, []*storage.Object, error) {
	// Try to get master event first
	master, err := h.store.GetMasterEvent(ctx, calendarID, uid)
	if err != nil {
		// If not found as master, try as regular object
		obj, err := h.store.GetObject(ctx, calendarID, uid)
		if err != nil {
			return nil, nil, err
		}
		// Not a recurring event, return as master with no exceptions
		return obj, nil, nil
	}

	// Get exceptions
	exceptions, err := h.store.GetEventExceptions(ctx, calendarID, uid)
	if err != nil {
		h.logger.Warn().Err(err).
			Str("uid", uid).
			Msg("failed to get exceptions, returning master only")
		return master, nil, nil
	}

	return master, exceptions, nil
}
