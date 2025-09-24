package caldav

import (
	"context"
	"encoding/xml"
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

	existing, _ := h.store.GetObject(r.Context(), calendarID, uid)

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

	compType, err := ical.DetectICSComponent(raw)
	if err != nil {
		h.logger.Error().Err(err).Msg("unsupported calendar component in PUT")
		http.Error(w, "unsupported calendar component", http.StatusUnsupportedMediaType)
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

	wantNew := r.Header.Get("If-None-Match") == "*"
	match := common.TrimQuotes(r.Header.Get("If-Match"))

	if wantNew && existing != nil {
		h.logger.Debug().Str("uid", uid).Msg("precondition failed - object exists")
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}
	if match != "" && existing != nil && existing.ETag != match {
		h.logger.Debug().
			Str("uid", uid).
			Str("expected_etag", match).
			Str("actual_etag", existing.ETag).
			Msg("precondition failed - etag mismatch")
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}

	obj := &storage.Object{
		CalendarID: calendarID,
		UID:        uid,
		Data:       string(ics),
		Component:  compType,
	}
	if err := h.store.PutObject(r.Context(), obj); err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("PutObject failed")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	_, _, err = h.store.RecordChange(r.Context(), calendarID, uid, false)
	if err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("RecordChange failed")
	}

	w.Header().Set("ETag", `"`+obj.ETag+`"`)
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

	match := common.TrimQuotes(r.Header.Get("If-Match"))
	if err := h.store.DeleteObject(r.Context(), calendarID, uid, match); err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("failed to delete object")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	_, _, err = h.store.RecordChange(r.Context(), calendarID, uid, true)
	if err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("RecordChange failed for DELETE")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) calendarExists(ctx context.Context, owner, uri string) bool {
	cals, err := h.store.ListCalendarsByOwnerUser(ctx, owner)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calendar", uri).
			Msg("failed to check if calendar exists")
		return false
	}
	for _, c := range cals {
		if c.URI == uri {
			return true
		}
	}
	return false
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
					return colorProp.Text
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
