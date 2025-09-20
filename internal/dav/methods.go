package dav

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/ical"
)

// HandleGet returns the raw iCalendar object by UID path.
func (h *Handlers) HandleGet(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := h.splitCalendarPath(r.URL.Path)
	if owner == "" || len(rest) == 0 {
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !safeSegment(calURI) || !safeSegment(uid) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := mustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, calURI, calOwner); !ok {
		return
	}
	obj, err := h.store.GetObject(r.Context(), calendarID, uid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// ETag conditional GET
	inm := trimQuotes(r.Header.Get("If-None-Match"))
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
	owner, calURI, rest := h.splitCalendarPath(r.URL.Path)
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

	if !safeSegment(calURI) || !safeSegment(uid) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := mustPrincipal(r.Context())

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

	// Detect top-level component: VEVENT, VTODO, or VJOURNAL
	compType, err := detectICSComponent(raw)
	if err != nil {
		http.Error(w, "unsupported calendar component", http.StatusUnsupportedMediaType)
		return
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
	match := trimQuotes(r.Header.Get("If-Match"))

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
	owner, calURI, rest := h.splitCalendarPath(r.URL.Path)
	if owner == "" || len(rest) == 0 {
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !safeSegment(calURI) || !safeSegment(uid) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	calendarID, calOwner, err := h.resolveCalendar(r.Context(), owner, calURI)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pr := mustPrincipal(r.Context())
	if pr.UserID != calOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil || !eff.CanDelete() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	match := trimQuotes(r.Header.Get("If-Match"))
	if err := h.store.DeleteObject(r.Context(), calendarID, uid, match); err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	// Record deletion change
	_, _, _ = h.store.RecordChange(r.Context(), calendarID, uid, true)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) HandleMkcol(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := h.splitCalendarPath(r.URL.Path)
	if owner == "" || calURI == "" || len(rest) != 0 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	pr := mustPrincipal(r.Context())
	if pr.UserID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.Error(w, "MKCOL not supported; provision calendars in storage", http.StatusForbidden)
}

func (h *Handlers) HandleProppatch(w http.ResponseWriter, r *http.Request) {
	owner, calURI, rest := h.splitCalendarPath(r.URL.Path)
	if owner == "" || calURI == "" || len(rest) != 0 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if !safeSegment(calURI) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	pr := mustPrincipal(r.Context())
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

func (h *Handlers) HandleACL(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "ACLs managed via LDAP groups", http.StatusForbidden)
}

// detectICSComponent inspects the ICS payload to find the first top-level component
// under VCALENDAR and returns one of: "VEVENT", "VTODO", "VJOURNAL".
// It tolerates CRLF/CR/newlines and folded lines. It does not expand recurrence.
func detectICSComponent(data []byte) (string, error) {
	// Quick sanity check: must contain VCALENDAR wrapper
	s := string(data)
	if !containsFoldInsensitive(s, "BEGIN:VCALENDAR") || !containsFoldInsensitive(s, "END:VCALENDAR") {
		return "", errors.New("not a VCALENDAR")
	}

	// Normalize newlines to \n for scanning
	norm := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	norm = bytes.ReplaceAll(norm, []byte("\r"), []byte("\n"))
	lines := bytes.Split(norm, []byte("\n"))

	// Unfold lines (RFC5545): lines that begin with space or tab are continuations
	var unf []string
	for i := 0; i < len(lines); i++ {
		line := string(lines[i])
		if i == 0 {
			unf = append(unf, line)
			continue
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			// continuation of previous line (strip leading WSP)
			if len(unf) == 0 {
				unf = append(unf, strings.TrimLeft(line, " \t"))
			} else {
				unf[len(unf)-1] += strings.TrimLeft(line, " \t")
			}
		} else {
			unf = append(unf, line)
		}
	}

	// Scan inside VCALENDAR for first BEGIN:VEVENT|VTODO|VJOURNAL
	inVC := false
	for _, l := range unf {
		ll := strings.TrimSpace(strings.ToUpper(l))
		switch {
		case ll == "BEGIN:VCALENDAR":
			inVC = true
		case ll == "END:VCALENDAR":
			inVC = false
		default:
			if inVC {
				if strings.HasPrefix(ll, "BEGIN:VEVENT") {
					return "VEVENT", nil
				}
				if strings.HasPrefix(ll, "BEGIN:VTODO") {
					return "VTODO", nil
				}
				if strings.HasPrefix(ll, "BEGIN:VJOURNAL") {
					return "VJOURNAL", nil
				}
			}
		}
	}

	// If none found, reject (we don't support other components like VFREEBUSY as stored objects)
	return "", errors.New("unsupported component")
}

// containsFoldInsensitive checks if s contains token case-insensitively, tolerant of line folding.
func containsFoldInsensitive(s, token string) bool {
	return strings.Contains(strings.ToUpper(s), strings.ToUpper(token))
}
