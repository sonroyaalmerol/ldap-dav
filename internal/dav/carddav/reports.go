package carddav

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (h *Handlers) ReportAddressbookQuery(w http.ResponseWriter, r *http.Request, q common.AddressbookQuery) {
	owner, abURI, _ := splitResourcePath(r.URL.Path, h.basePath)
	addressbookID, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("failed to resolve addressbook in addressbook-query")
		http.NotFound(w, r)
		return
	}

	pr := common.MustPrincipal(r.Context())
	props := common.ParsePropRequest(q.Prop)

	if ok := h.mustCanRead(w, r.Context(), pr, abURI, abOwner); !ok {
		return
	}

	filterProps := common.ExtractPropFilterNames(q.Filter)

	if strings.HasPrefix(addressbookID, "ldap_") {
		dir := h.addressbookDirs[abURI]
		if dir == nil {
			http.NotFound(w, r)
			return
		}
		contacts, err := dir.ListContacts(r.Context(), filterProps)
		if err != nil {
			http.Error(w, "ldap error", http.StatusInternalServerError)
			return
		}
		var resps []common.Response
		for _, ct := range contacts {
			hrefStr := common.JoinURL(h.basePath, "addressbooks", owner, abURI, ct.ID+".vcf")
			resps = append(resps, buildReportResponseLDAP(hrefStr, props, &ct))
		}
		ms := common.MultiStatus{Responses: resps}
		_ = common.ServeMultiStatus(w, &ms)
		return
	}

	contacts, err := h.store.ListContactsByFilter(r.Context(), addressbookID, filterProps)
	if err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Msg("failed to list contacts in addressbook-query")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response
	for _, contact := range contacts {
		hrefStr := common.JoinURL(h.basePath, "addressbooks", owner, abURI, contact.UID+".vcf")
		resps = append(resps, buildReportResponse(hrefStr, props, contact))
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for addressbook-query")
	}
}

func (h *Handlers) ReportAddressbookMultiget(w http.ResponseWriter, r *http.Request, mg common.AddressbookMultiget) {
	props := common.ParsePropRequest(mg.Prop)
	var resps []common.Response
	for _, hrefStr := range mg.Hrefs {
		owner, abURI, rest := splitResourcePath(hrefStr, h.basePath)
		if owner == "" || len(rest) == 0 {
			continue
		}
		filename := rest[len(rest)-1]
		uid := strings.TrimSuffix(filename, filepath.Ext(filename))

		addressbookID, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
		if err != nil {
			h.logger.Debug().Err(err).
				Str("owner", owner).
				Str("addressbook", abURI).
				Msg("failed to resolve addressbook in multiget")
			continue
		}

		if strings.HasPrefix(addressbookID, "ldap_") {
			dir := h.addressbookDirs[abURI]
			if dir == nil {
				h.logger.Debug().Err(err).
					Str("addressbookID", addressbookID).
					Str("uid", uid).
					Msg("failed to get contact in multiget")
				return
			}
			contact, err := dir.GetContact(r.Context(), uid)
			if err != nil {
				h.logger.Debug().Err(err).
					Str("addressbookID", addressbookID).
					Str("uid", uid).
					Msg("failed to get contact in multiget")
				continue
			}
			resps = append(resps, buildReportResponseLDAP(hrefStr, props, contact))
			continue
		}

		pr := common.MustPrincipal(r.Context())
		okRead, err := h.aclCheckRead(r.Context(), pr, abURI, abOwner)
		if err != nil || !okRead {
			h.logger.Debug().Err(err).
				Bool("can_read", okRead).
				Str("user", pr.UserID).
				Str("addressbook", abURI).
				Msg("ACL check failed in multiget")
			continue
		}
		contact, err := h.store.GetContact(r.Context(), addressbookID, uid)
		if err != nil {
			h.logger.Debug().Err(err).
				Str("addressbookID", addressbookID).
				Str("uid", uid).
				Msg("failed to get contact in multiget")
			continue
		}
		resps = append(resps, buildReportResponse(hrefStr, props, contact))
	}
	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for addressbook-multiget")
	}
}

func buildReportResponse(hrefStr string, props common.PropRequest, contact *storage.Contact) common.Response {
	resp := common.Response{
		Hrefs: []common.Href{{Value: hrefStr}},
	}
	_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/vcard; charset=utf-8"})
	if props.AddressData {
		type AddressData struct {
			XMLName xml.Name `xml:"urn:ietf:params:xml:ns:carddav address-data"`
			Text    string   `xml:",chardata"`
		}
		_ = resp.EncodeProp(http.StatusOK, AddressData{Text: contact.Data})
	}
	if props.GetETag && contact.ETag != "" {
		_ = resp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(contact.ETag)})
	}
	if !contact.UpdatedAt.IsZero() {
		_ = resp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(contact.UpdatedAt)})
	}
	return resp
}

func (h *Handlers) ReportSyncCollection(w http.ResponseWriter, r *http.Request, sc common.SyncCollection) {
	owner, abURI, _ := splitResourcePath(r.URL.Path, h.basePath)
	addressbookID, abOwner, err := h.resolveAddressbook(r.Context(), owner, abURI)
	if err != nil {
		h.logger.Error().Err(err).
			Str("owner", owner).
			Str("addressbook", abURI).
			Msg("failed to resolve addressbook in sync-collection")
		http.NotFound(w, r)
		return
	}

	if strings.HasPrefix(addressbookID, "ldap_") {
		ms := common.MultiStatus{
			Responses: nil,
			SyncToken: "seq:0",
		}
		_ = common.ServeMultiStatus(w, &ms)
		return
	}

	pr := common.MustPrincipal(r.Context())
	if ok := h.mustCanRead(w, r.Context(), pr, abURI, abOwner); !ok {
		return
	}

	props := common.ParsePropRequest(sc.Prop)

	curToken, _, err := h.store.GetAddressbookSyncInfo(r.Context(), addressbookID)
	if err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Msg("failed to get sync info")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	sinceSeq := int64(0)
	if sc.SyncToken != "" {
		if ss, ok := common.ParseSeqToken(sc.SyncToken); ok {
			sinceSeq = ss
		}
	}
	limit := 0
	if sc.Limit != nil && sc.Limit.NResults > 0 {
		limit = sc.Limit.NResults
	}
	changes, _, err := h.store.ListAddressbookChangesSince(r.Context(), addressbookID, sinceSeq, limit)
	if err != nil {
		h.logger.Error().Err(err).
			Str("addressbookID", addressbookID).
			Int64("since", sinceSeq).
			Int("limit", limit).
			Msg("failed to list changes")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	baseHref := r.URL.Path
	if !strings.HasSuffix(baseHref, "/") {
		baseHref += "/"
	}

	for _, ch := range changes {
		hrefStr := baseHref + ch.UID + ".vcf"
		if ch.Deleted {
			resp := common.Response{
				Hrefs: []common.Href{{Value: hrefStr}},
			}
			resp.Status = &common.Status{Code: http.StatusNotFound}
			resps = append(resps, resp)
		} else {
			resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
			var contact *storage.Contact
			var getErr error
			needContact := props.AddressData || props.GetETag
			if needContact {
				contact, getErr = h.store.GetContact(r.Context(), addressbookID, ch.UID)
				if getErr != nil {
					h.logger.Debug().Err(getErr).
						Str("addressbookID", addressbookID).
						Str("uid", ch.UID).
						Msg("contact disappeared between change listing and fetch")
					resp.Status = &common.Status{Code: http.StatusNotFound}
					resps = append(resps, resp)
					continue
				}
			}
			if props.GetETag && contact != nil && contact.ETag != "" {
				_ = resp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(contact.ETag)})
			}
			if props.AddressData && contact != nil {
				type AddressData struct {
					XMLName xml.Name `xml:"urn:ietf:params:xml:ns:carddav address-data"`
					Text    string   `xml:",chardata"`
				}
				_ = resp.EncodeProp(http.StatusOK, AddressData{Text: contact.Data})
			}
			resps = append(resps, resp)
		}
	}

	ms := common.MultiStatus{
		Responses: resps,
		SyncToken: curToken,
	}
	if limit > 0 && len(changes) == limit {
		ms.NumberOfMatchesWithinLimits = fmt.Sprintf("%d", len(changes))
	}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for sync-collection")
	}
}
