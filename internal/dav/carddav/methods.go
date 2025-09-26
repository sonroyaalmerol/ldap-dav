package carddav

import (
	"encoding/xml"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/pkg/vcard"
)

func (h *Handlers) GetCapabilities() string {
	return "addressbook"
}

func (h *Handlers) HandleHead(w http.ResponseWriter, r *http.Request) {
	hrw := &headResponseWriter{ResponseWriter: w}
	h.HandleGet(hrw, r)
}

func (h *Handlers) HandleGet(w http.ResponseWriter, r *http.Request) {
	owner, abURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || len(rest) == 0 {
		h.logger.Debug().Str("path", r.URL.Path).Msg("GET request with invalid path")
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(abURI) || !common.SafeSegment(uid) {
		h.logger.Error().
			Str("addressbook", abURI).
			Str("uid", uid).
			Msg("GET request with unsafe path segments")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	addressbookID, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("failed to resolve addressbook in GET")
		http.NotFound(w, r)
		return
	}

	if strings.HasPrefix(addressbookID, "ldap_") {
		dir := h.addressbookDirs[abURI]
		if dir == nil {
			h.logger.Error().Err(err).
				Str("owner", owner).
				Str("addressbook", abURI).
				Msg("failed to resolve addressbook ldap in GET")
			http.NotFound(w, r)
			return
		}
		contact, err := dir.GetContact(r.Context(), uid)
		if err != nil {
			h.logger.Error().Err(err).
				Str("owner", owner).
				Str("addressbook", abURI).
				Msg("failed to resolve addressbook in GET")
			http.NotFound(w, r)
			return
		}

		etag := computeStableETag(contact)
		inm := common.TrimQuotes(r.Header.Get("If-None-Match"))
		if inm != "" && inm == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
		w.Header().Set("ETag", `"`+etag+`"`)
		_, _ = io.WriteString(w, contact.VCardData)
		return
	}

	pr := common.MustPrincipal(r.Context())

	if pr.UserID != abOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("ACL check failed in GET")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.Read {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("insufficient DAV:read privileges for GET")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	contact, err := h.store.GetContact(r.Context(), addressbookID, uid)
	if err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Str("uid", uid).
			Msg("failed to get contact in GET")
		http.NotFound(w, r)
		return
	}

	inm := common.TrimQuotes(r.Header.Get("If-None-Match"))
	if inm != "" && inm == contact.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
	w.Header().Set("ETag", `"`+contact.ETag+`"`)
	if !contact.UpdatedAt.IsZero() {
		w.Header().Set("Last-Modified", contact.UpdatedAt.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"))
	}
	_, _ = io.WriteString(w, contact.Data)
}

func (h *Handlers) HandlePut(w http.ResponseWriter, r *http.Request) {
	owner, abURI, rest := splitResourcePath(r.URL.Path, h.basePath)
	if owner == "" || len(rest) == 0 {
		h.logger.Debug().Str("path", r.URL.Path).Msg("PUT request with invalid path")
		http.NotFound(w, r)
		return
	}
	filename := rest[len(rest)-1]
	if !strings.HasSuffix(strings.ToLower(filename), ".vcf") {
		h.logger.Error().Str("filename", filename).Msg("PUT request with invalid filename")
		http.Error(w, "bad contact name", http.StatusBadRequest)
		return
	}
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(abURI) || !common.SafeSegment(uid) {
		h.logger.Error().
			Str("addressbook", abURI).
			Str("uid", uid).
			Msg("PUT request with unsafe path segments")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	addressbookID, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("failed to resolve addressbook in PUT")
		http.NotFound(w, r)
		return
	}

	if strings.HasPrefix(addressbookID, "ldap_") {
		h.logger.Debug().Str("addressbook", abURI).Msg("PUT denied - LDAP addressbooks are read-only")
		http.Error(w, "method not allowed - LDAP addressbooks are read-only", http.StatusMethodNotAllowed)
		return
	}

	pr := common.MustPrincipal(r.Context())

	existing, _ := h.store.GetContact(r.Context(), addressbookID, uid)

	if pr.UserID != abOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("ACL check failed in PUT")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if existing == nil {
			if !eff.Bind {
				h.logger.Debug().
					Str("user", pr.UserID).
					Str("addressbook", abURI).
					Msg("insufficient DAV:bind privileges for creating new contact")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		} else {
			if !eff.WriteContent {
				h.logger.Debug().
					Str("user", pr.UserID).
					Str("addressbook", abURI).
					Msg("insufficient DAV:write-content privileges for modifying existing contact")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
	}

	maxVCard := h.cfg.HTTP.MaxVCFBytes
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxVCard+1))
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

	if maxVCard > 0 && int64(len(raw)) > maxVCard {
		h.logger.Error().
			Int("size", len(raw)).
			Int64("max", maxVCard).
			Msg("payload too large in PUT")
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Validate vCard data
	if err := vcard.ValidateVCard(raw); err != nil {
		h.logger.Error().Err(err).Msg("invalid vCard in PUT")
		http.Error(w, "invalid vcard", http.StatusBadRequest)
		return
	}

	vcard, err := vcard.NormalizeVCard(raw, "")
	if err != nil {
		h.logger.Error().Err(err).Bytes("raw_vcard", raw).Msg("normalize vcard failed")
		http.Error(w, "invalid vcard", http.StatusBadRequest)
		return
	}

	wantNew := r.Header.Get("If-None-Match") == "*"
	match := common.TrimQuotes(r.Header.Get("If-Match"))

	if wantNew && existing != nil {
		h.logger.Debug().Str("uid", uid).Msg("precondition failed - contact exists")
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

	contact := &storage.Contact{
		AddressbookID: addressbookID,
		UID:           uid,
		Data:          string(vcard),
	}
	if err := h.store.PutContact(r.Context(), contact); err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Str("uid", uid).
			Msg("PutContact failed")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	_, _, err = h.store.RecordAddressbookChange(r.Context(), addressbookID, uid, false)
	if err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Str("uid", uid).
			Msg("RecordAddressbookChange failed")
	}

	w.Header().Set("ETag", `"`+contact.ETag+`"`)
	if existing == nil {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handlers) HandleDelete(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, abURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if owner == "" || abURI == "" {
		if o2, ab2, ok := tryAddressbookShorthand(r.URL.Path, h.basePath, pr.UserID); ok {
			owner, abURI, rest = o2, ab2, nil
		}
	}

	if owner == "" || abURI == "" {
		h.logger.Error().
			Str("path", r.URL.Path).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("DELETE request with invalid path")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if len(rest) == 0 {
		if !common.SafeCollectionName(abURI) {
			h.logger.Error().Str("addressbook", abURI).Msg("unsafe collection name in DELETE")
			http.Error(w, "bad collection name", http.StatusBadRequest)
			return
		}

		if pr.UserID != owner {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("insufficient privileges for DELETE addressbook")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := h.store.DeleteAddressbook(owner, abURI); err != nil {
			h.logger.Error().Err(err).
				Str("owner", owner).
				Str("addressbook", abURI).
				Msg("failed to delete addressbook")
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	filename := rest[len(rest)-1]
	uid := strings.TrimSuffix(filename, filepath.Ext(filename))

	if !common.SafeSegment(abURI) || !common.SafeSegment(uid) {
		h.logger.Error().
			Str("addressbook", abURI).
			Str("uid", uid).
			Msg("unsafe path segments in DELETE contact")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	addressbookID, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("failed to resolve addressbook in DELETE")
		http.NotFound(w, r)
		return
	}

	if strings.HasPrefix(addressbookID, "ldap_") {
		h.logger.Debug().
			Str("addressbook", abURI).
			Msg("DELETE request denied - LDAP addressbooks are read-only")
		http.Error(w, "method not allowed - LDAP addressbooks are read-only", http.StatusMethodNotAllowed)
		return
	}

	if pr.UserID != abOwner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("ACL check failed in DELETE contact")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.Unbind {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("insufficient DAV:unbind privileges for DELETE contact")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	match := common.TrimQuotes(r.Header.Get("If-Match"))
	if err := h.store.DeleteContact(r.Context(), addressbookID, uid, match); err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Str("uid", uid).
			Msg("failed to delete contact")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	_, _, err = h.store.RecordAddressbookChange(r.Context(), addressbookID, uid, true)
	if err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Str("uid", uid).
			Msg("RecordAddressbookChange failed for DELETE")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) HandleMkcol(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, abURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if strings.HasPrefix(abURI, "ldap_") {
		http.Error(w, "method not allowed - cannot create LDAP collections", http.StatusMethodNotAllowed)
		return
	}

	if owner == "" || abURI == "" || len(rest) != 0 {
		if o2, ab2, ok := tryAddressbookShorthand(r.URL.Path, h.basePath, pr.UserID); ok {
			owner, abURI, rest = o2, ab2, nil
		} else {
			h.logger.Error().Str("path", r.URL.Path).Msg("MKCOL with invalid path")
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
	}

	if !common.SafeCollectionName(abURI) {
		h.logger.Error().Str("addressbook", abURI).Msg("unsafe collection name in MKCOL")
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
		Description  *string  `xml:"urn:ietf:params:xml:ns:carddav addressbook-description"`
		ResourceType struct {
			Addressbook *struct{} `xml:"urn:ietf:params:xml:ns:carddav addressbook"`
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

	isAddressbook := mkcolReq.Set != nil && mkcolReq.Set.Prop.ResourceType.Addressbook != nil
	if !isAddressbook {
		h.logger.Error().Msg("MKCOL with unsupported collection type")
		http.Error(w, "unsupported collection type", http.StatusUnsupportedMediaType)
		return
	}

	if h.addressbookExists(r.Context(), owner, abURI) {
		h.logger.Debug().
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("addressbook already exists in MKCOL")
		http.Error(w, "conflict", http.StatusConflict)
		return
	}

	var displayName string
	var description string

	if mkcolReq.Set != nil {
		if mkcolReq.Set.Prop.DisplayName != nil {
			displayName = *mkcolReq.Set.Prop.DisplayName
		}
		if mkcolReq.Set.Prop.Description != nil {
			description = *mkcolReq.Set.Prop.Description
		}
	}

	newAB := storage.Addressbook{
		OwnerUserID: owner,
		URI:         abURI,
		DisplayName: displayName,
		Description: description,
	}
	if err := h.store.CreateAddressbook(newAB, "", description); err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("failed to create addressbook in MKCOL")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handlers) HandleMkcalendar(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (h *Handlers) HandleProppatch(w http.ResponseWriter, r *http.Request) {
	owner, abURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if strings.HasPrefix(abURI, "ldap_") {
		http.Error(w, "method not allowed - LDAP addressbooks are read-only", http.StatusMethodNotAllowed)
		return
	}

	if owner == "" || abURI == "" || len(rest) != 0 {
		h.logger.Error().Str("path", r.URL.Path).Msg("PROPPATCH with invalid path")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if !common.SafeSegment(abURI) {
		h.logger.Error().Str("addressbook", abURI).Msg("unsafe path in PROPPATCH")
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("ACL check failed in PROPPATCH")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !eff.WriteProps {
			h.logger.Debug().
				Str("user", pr.UserID).
				Str("addressbook", abURI).
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
	var displayNameStatus int = http.StatusOK

	if okXML && req.Set != nil {
		if req.Set.Prop.DisplayName != nil {
			newName = req.Set.Prop.DisplayName
		}
	}

	if okXML && req.Remove != nil {
		if req.Remove.Prop.DisplayName != nil {
			newName = nil
		}
	}

	if newName != nil || (okXML && req.Remove != nil && req.Remove.Prop.DisplayName != nil) {
		if err := h.store.UpdateAddressbookDisplayName(r.Context(), owner, abURI, newName); err != nil {
			h.logger.Error().Err(err).Msg("Failed to update addressbook display name")
			displayNameStatus = http.StatusInternalServerError
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

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPPATCH")
	}
}

func (h *Handlers) HandleReport(w http.ResponseWriter, r *http.Request) {
	pr := common.MustPrincipal(r.Context())
	owner, abURI, rest := splitResourcePath(r.URL.Path, h.basePath)

	if owner != "" && abURI != "" && len(rest) == 0 {
		_, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
		if err != nil {
			h.logger.Error().Err(err).
				Str("owner", owner).
				Str("addressbook", abURI).
				Msg("failed to resolve addressbook in REPORT")
			http.NotFound(w, r)
			return
		}

		if pr.UserID != abOwner {
			eff, err := h.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, abURI)
			if err != nil {
				h.logger.Error().Err(err).
					Str("user", pr.UserID).
					Str("addressbook", abURI).
					Msg("ACL check failed in REPORT")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if !eff.Read {
				h.logger.Debug().
					Str("user", pr.UserID).
					Str("addressbook", abURI).
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
	case common.NSCardDAV + " addressbook-query":
		var q common.AddressbookQuery
		if err := xml.Unmarshal(body, &q); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal addressbook-query")
		}
		h.ReportAddressbookQuery(w, r, q)
	case common.NSCardDAV + " addressbook-multiget":
		var mg common.AddressbookMultiget
		if err := xml.Unmarshal(body, &mg); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal addressbook-multiget")
		}
		h.ReportAddressbookMultiget(w, r, mg)
	case common.NSDAV + " sync-collection":
		var sc common.SyncCollection
		if err := xml.Unmarshal(body, &sc); err != nil {
			h.logger.Error().Err(err).Msg("failed to unmarshal sync-collection")
		}
		h.ReportSyncCollection(w, r, sc)
	default:
		h.logger.Error().
			Str("namespace", root.XMLName.Space).
			Str("local", root.XMLName.Local).
			Msg("unsupported REPORT type")
		http.Error(w, "unsupported REPORT", http.StatusBadRequest)
	}
}
