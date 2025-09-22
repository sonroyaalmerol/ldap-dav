package caldav

import (
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

// HandleGet returns the raw iCalendar object by UID path.
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

	// ETag conditional GET
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

// HandlePut creates or updates an iCalendar object and detects its component type.
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

	// ACL: owner or create/edit
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

	// Normalize ICS
	ics, err := ical.NormalizeICS(raw)
	if err != nil {
		h.logger.Error().Err(err).Bytes("raw_ics", raw).Msg("normalize ics failed")
		http.Error(w, "invalid ical", http.StatusBadRequest)
		return
	}

	// Precondition headers
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
		Component:  compType, // "VEVENT", "VTODO", or "VJOURNAL"
	}
	if err := h.store.PutObject(r.Context(), obj); err != nil {
		h.logger.Error().Err(err).
			Str("calendarID", calendarID).
			Str("uid", uid).
			Msg("PutObject failed")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	// Record change (add/update)
	_, _, _ = h.store.RecordChange(r.Context(), calendarID, uid, false)

	w.Header().Set("ETag", `"`+obj.ETag+`"`)
	if existing == nil {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleDelete removes an iCalendar object by UID (respecting If-Match) and records a change.
func (h *Handlers) HandleDelete(w http.ResponseWriter, r *http.Request) {
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
	// Record deletion change
	_, _, _ = h.store.RecordChange(r.Context(), calendarID, uid, true)
	w.WriteHeader(http.StatusNoContent)
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

	// Parse a minimal PROPPATCH setting DAV:displayname
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	type setRemove struct {
		XMLName xml.Name
		Prop    struct {
			DisplayName *string `xml:"displayname"`
		} `xml:"prop"`
	}
	var req struct {
		XMLName xml.Name   `xml:"DAV: propertyupdate"`
		Set     *setRemove `xml:"set"`
		Remove  *setRemove `xml:"remove"`
	}
	okXML := true
	if err := xml.Unmarshal(body, &req); err != nil {
		okXML = false
	}
	var newName *string
	if okXML && req.Set != nil && req.Set.Prop.DisplayName != nil {
		newName = req.Set.Prop.DisplayName
	}
	if okXML && req.Remove != nil && req.Remove.Prop.DisplayName != nil {
		// Remove displayname -> set NULL
		newName = nil
	}
	if newName != nil || (okXML && req.Remove != nil) {
		_ = h.store.UpdateCalendarDisplayName(r.Context(), owner, calURI, newName)
	}
	// Minimal multistatus response
	w.WriteHeader(207)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><d:multistatus xmlns:d="DAV:"><d:response><d:status>HTTP/1.1 200 OK</d:status></d:response></d:multistatus>`)
}

func (h *Handlers) HandleMkcol(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || calURI == "" || len(rest) != 0 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if !common.SafeSegment(calURI) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	// Check if calendar already exists
	if _, err := h.loadCalendarByOwnerURI(r.Context(), owner, calURI); err == nil {
		http.Error(w, "calendar already exists", http.StatusConflict)
		return
	}

	// Parse request body to determine if this is a calendar creation
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()

	var displayName string
	var description string
	isCalendar := false

	if len(body) > 0 {
		// Check if this is an MKCOL request with calendar resourcetype
		var req struct {
			XMLName xml.Name `xml:"DAV: mkcol"`
			Set     struct {
				Prop struct {
					ResourceType struct {
						Collection *struct{} `xml:"DAV: collection"`
						Calendar   *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
					} `xml:"DAV: resourcetype"`
					DisplayName         *string `xml:"DAV: displayname"`
					CalendarDescription *string `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: set"`
		}

		if err := xml.Unmarshal(body, &req); err == nil {
			// Check if calendar resourcetype is specified
			if req.Set.Prop.ResourceType.Calendar != nil {
				isCalendar = true
			}
			if req.Set.Prop.DisplayName != nil {
				displayName = *req.Set.Prop.DisplayName
			}
			if req.Set.Prop.CalendarDescription != nil {
				description = *req.Set.Prop.CalendarDescription
			}
		}
	}

	// Default to calendar creation (CalDAV servers typically only create calendars)
	if !isCalendar {
		isCalendar = true
	}

	if !isCalendar {
		http.Error(w, "only calendar collections supported", http.StatusForbidden)
		return
	}

	// Set defaults if not provided
	if displayName == "" {
		displayName = calURI
	}

	// Create the calendar using your existing method signature
	calendar := storage.Calendar{
		URI:         calURI,
		DisplayName: displayName,
		OwnerUserID: owner,
	}

	if err := h.store.CreateCalendar(calendar, "", description); err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("calURI", calURI).
			Msg("CreateCalendar failed")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) HandleMkcalendar(w http.ResponseWriter, r *http.Request) {
	// Parse MKCALENDAR request body and convert to MKCOL format
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()

	var displayName string
	var description string

	if len(body) > 0 {
		// Parse MKCALENDAR XML body
		var req struct {
			XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav mkcalendar"`
			Set     struct {
				Prop struct {
					DisplayName         *string `xml:"DAV: displayname"`
					CalendarDescription *string `xml:"urn:ietf:params:xml:ns:caldav calendar-description"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: set"`
		}

		if err := xml.Unmarshal(body, &req); err == nil {
			if req.Set.Prop.DisplayName != nil {
				displayName = *req.Set.Prop.DisplayName
			}
			if req.Set.Prop.CalendarDescription != nil {
				description = *req.Set.Prop.CalendarDescription
			}
		}
	}

	// Convert to MKCOL format
	mkcolBody := `<?xml version="1.0" encoding="utf-8" ?>
<D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:set>
    <D:prop>
      <D:resourcetype>
        <D:collection/>
        <C:calendar/>
      </D:resourcetype>`

	if displayName != "" {
		mkcolBody += fmt.Sprintf(`
      <D:displayname>%s</D:displayname>`, displayName)
	}

	if description != "" {
		mkcolBody += fmt.Sprintf(`
      <C:calendar-description>%s</C:calendar-description>`, description)
	}

	mkcolBody += `
    </D:prop>
  </D:set>
</D:mkcol>`

	// Create new request with converted body
	r.Body = io.NopCloser(strings.NewReader(mkcolBody))

	// Delegate to MKCOL handler
	h.HandleMkcol(w, r)
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
