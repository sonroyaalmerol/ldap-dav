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
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}
	obj, err := h.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
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
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	if !strings.HasSuffix(strings.ToLower(filename), ".ics") {
		http.Error(w, "bad object name", http.StatusBadRequest)
		return
	}
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.logger.Debug().
		Str("owner", owner).
		Str("calURI", calURI).
		Str("calendarID", calendarID).
		Str("calOwner", calOwner).
		Msg("resolved calendar for PUT")

	pr := common.MustPrincipal(r.Context())

	if pr.UserID != calOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil || !(eff.CanCreate() || eff.CanEdit()) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	maxICS := h.cfg.HTTP.MaxICSBytes
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxICS+1))
	_ = r.Body.Close()
	if len(raw) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	if maxICS > 0 && int64(len(raw)) > maxICS {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	compType, err := ical.DetectICSComponent(raw)
	if err != nil {
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

	existing, _ := h.store.GetObject(r.Context(), calendarID, uid)
	if wantNew && existing != nil {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}
	if match != "" && existing != nil && existing.ETag != match {
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
	_, _, _ = h.store.RecordChange(r.Context(), calendarID, uid, false)

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
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if len(rest) == 0 {
		if !common.SafeCollectionName(calURI) {
			http.Error(w, "bad collection name", http.StatusBadRequest)
			return
		}

		if pr.UserID != owner {
			eff, err := h.aclProv.Effective(
				r.Context(),
				&directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display},
				calURI,
			)
			if err != nil || !eff.CanDelete() {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}

		if err := h.store.DeleteCalendar(owner, calURI); err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(calURI) || !common.SafeSegment(uid) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if pr.UserID != calOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil || !eff.CanDelete() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	match := common.TrimQuotes(r.Header.Get("If-Match"))
	if err := h.store.DeleteObject(r.Context(), calendarID, uid, match); err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	_, _, _ = h.store.RecordChange(r.Context(), calendarID, uid, true)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) calendarExists(ctx context.Context, owner, uri string) bool {
	cals, err := h.store.ListCalendarsByOwnerUser(ctx, owner)
	if err != nil {
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
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
	}

	if !common.SafeCollectionName(calURI) {
		http.Error(w, "bad collection name", http.StatusBadRequest)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
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
		_ = xml.Unmarshal(body, &mkcolReq)
	}

	isCalendar := mkcolReq.Set != nil && mkcolReq.Set.Prop.ResourceType.Calendar != nil
	if !isCalendar {
		http.Error(w, "unsupported collection type", http.StatusUnsupportedMediaType)
		return
	}

	if h.calendarExists(r.Context(), owner, calURI) {
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
		color = "#3174ad" // Fall back to default
	}

	newCal := storage.Calendar{
		OwnerUserID: owner,
		URI:         calURI,
		DisplayName: displayName,
		Description: description,
		Color:       color,
	}
	if err := h.store.CreateCalendar(newCal, "", description); err != nil {
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
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
	}

	if pr.UserID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if !common.SafeCollectionName(calURI) {
		http.Error(w, "bad collection name", http.StatusBadRequest)
		return
	}

	if h.calendarExists(r.Context(), owner, calURI) {
		http.Error(w, "conflict", http.StatusConflict)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
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
		if err := xml.Unmarshal(body, &mkcalReq); err == nil {
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
		color = "#3174ad" // Fall back to default
	}

	newCal := storage.Calendar{
		OwnerUserID: owner,
		URI:         calURI,
		DisplayName: displayName,
		Description: description,
		Color:       color,
	}
	if err := h.store.CreateCalendar(newCal, "", description); err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) HandleProppatch(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || calURI == "" || len(rest) != 0 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if !common.SafeSegment(calURI) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil || !eff.CanEdit() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
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
			newName = nil // Remove display name (set to empty)
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
					newColor = "#3174ad" // Reset to default color
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
		_ = resp.EncodeProp(displayNameStatus, common.DisplayName{Name: propValue})
	}

	if hasColorUpdate {
		if colorStatus == http.StatusOK {
			_ = resp.EncodeProp(colorStatus, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
				Text    string   `xml:",chardata"`
			}{Text: newColor})
		} else {
			_ = resp.EncodeProp(colorStatus, struct {
				XMLName xml.Name `xml:"http://apple.com/ns/ical/ calendar-color"`
			}{})
		}
	}

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	_ = common.ServeMultiStatus(w, &ms)
}

func (h *Handlers) HandleReport(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	_ = r.Body.Close()

	root := struct {
		XMLName xml.Name
	}{}
	if err := xml.Unmarshal(body, &root); err != nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}

	switch root.XMLName.Space + " " + root.XMLName.Local {
	case common.NSCalDAV + " calendar-query":
		var q common.CalendarQuery
		_ = xml.Unmarshal(body, &q)
		h.ReportCalendarQuery(w, r, q)
	case common.NSCalDAV + " calendar-multiget":
		var mg common.CalendarMultiget
		_ = xml.Unmarshal(body, &mg)
		h.ReportCalendarMultiget(w, r, mg)
	case common.NSDAV + " sync-collection":
		var sc common.SyncCollection
		_ = xml.Unmarshal(body, &sc)
		h.ReportSyncCollection(w, r, sc)
	case common.NSCalDAV + " free-busy-query":
		var fb common.FreeBusyQuery
		_ = xml.Unmarshal(body, &fb)
		h.ReportFreeBusyQuery(w, r, fb)
	default:
		http.Error(w, "unsupported REPORT", http.StatusBadRequest)
	}
}

func (h *Handlers) HandleACL(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "ACLs managed via LDAP groups", http.StatusForbidden)
}
