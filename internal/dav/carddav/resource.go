package carddav

import (
	"encoding/xml"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

type CardDAVResourceHandler struct {
	handlers *Handlers
	basePath string
}

func NewCardDAVResourceHandler(handlers *Handlers, basePath string) *CardDAVResourceHandler {
	return &CardDAVResourceHandler{
		handlers: handlers,
		basePath: basePath,
	}
}

func (c *CardDAVResourceHandler) SplitResourcePath(urlPath string) (owner, collection string, rest []string) {
	return splitResourcePath(urlPath, c.basePath)
}

func (c *CardDAVResourceHandler) PropfindHome(w http.ResponseWriter, r *http.Request, owner, depth string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		c.handlers.logger.Error().Str("path", r.URL.Path).Msg("PROPFIND home unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	home := common.AddressbookHome(c.basePath, owner)

	if u.UID != owner {
		c.handlers.logger.Debug().Str("user", u.UID).Str("owner", owner).Msg("PROPFIND home forbidden - user mismatch")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	c.handlers.ensurePersonalAddressbook(r.Context(), owner)

	owned, err := c.handlers.store.ListAddressbooksByOwnerUser(r.Context(), owner)
	if err != nil {
		c.handlers.logger.Error().Err(err).Str("owner", owner).Msg("failed to list owned addressbooks in PROPFIND home")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var resps []common.Response

	homeResp := common.Response{Hrefs: []common.Href{{Value: home}}}
	_ = homeResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
	_ = homeResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Addressbook Home"})
	_ = homeResp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
	_ = homeResp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})

	_ = homeResp.EncodeProp(http.StatusOK, c.buildSupportedPrivilegeSet())
	_ = homeResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
		Privilege: []common.Privilege{{All: &struct{}{}}},
	})
	_ = homeResp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))

	resps = append(resps, homeResp)

	if depth == "1" {
		// Add user's own addressbooks
		for _, ab := range owned {
			hrefStr := common.AddressbookPath(c.basePath, owner, ab.URI)
			resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
			_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Addressbook: &struct{}{}})
			_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: ab.DisplayName})
			_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
			_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
			_ = resp.EncodeProp(http.StatusOK, supportedReportSetValue())
			_ = resp.EncodeProp(http.StatusOK, struct {
				XMLName xml.Name `xml:"DAV: sync-token"`
				Text    string   `xml:",chardata"`
			}{Text: ab.CTag})
			_ = resp.EncodeProp(http.StatusOK, struct {
				XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
				Text    string   `xml:",chardata"`
			}{Text: ab.CTag})

			_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
				Privilege: []common.Privilege{{All: &struct{}{}}},
			})

			_ = resp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))
			resps = append(resps, resp)
		}

		// LDAP addressbooks
		for uri, dir := range c.handlers.addressbookDirs {
			abs, err := dir.ListAddressbooks(r.Context())
			if err != nil {
				c.handlers.logger.Error().Err(err).Str("ldap_ab", uri).Msg("failed to list LDAP addressbooks")
				continue
			}
			for _, ab := range abs {
				hrefStr := common.AddressbookPath(c.basePath, owner, ab.URI)
				resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
				_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Addressbook: &struct{}{}})
				_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: ab.Name})
				_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
				_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
				_ = resp.EncodeProp(http.StatusOK, supportedReportSetValue())

				// Read-only: privileges limited to read
				_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
					Privilege: []common.Privilege{{Read: &struct{}{}}},
				})
				_ = resp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))
				// Fixed sync token for read-only LDAP (no change tracking)
				_ = resp.EncodeProp(http.StatusOK, struct {
					XMLName xml.Name `xml:"DAV: sync-token"`
					Text    string   `xml:",chardata"`
				}{Text: "seq:0"})

				resps = append(resps, resp)
			}
		}
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus in PROPFIND home")
	}
}

func (c *CardDAVResourceHandler) PropfindCollection(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		c.handlers.logger.Error().Str("path", r.URL.Path).Msg("PROPFIND collection unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if u.UID != owner {
		c.handlers.logger.Debug().Str("user", u.UID).Str("owner", owner).Msg("PROPFIND collection forbidden - user mismatch")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if strings.HasPrefix(collection, "ldap_") {
		if _, ok := c.handlers.addressbookDirs[collection]; !ok {
			c.handlers.logger.Debug().Str("user", u.UID).Str("owner", owner).Msg("PROPFIND collection forbidden - user mismatch")
			http.NotFound(w, r)
			return
		}
		href := common.AddressbookPath(c.basePath, owner, collection)
		ownerHref := common.PrincipalURL(c.basePath, owner)

		var resps []common.Response

		resp := common.Response{Hrefs: []common.Href{{Value: href}}}
		_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Addressbook: &struct{}{}})
		_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: collection})
		_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: ownerHref}})
		_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: ownerHref}})
		_ = resp.EncodeProp(http.StatusOK, supportedReportSetValue())
		_ = resp.EncodeProp(http.StatusOK, c.buildSupportedPrivilegeSet())
		_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{Privilege: []common.Privilege{{Read: &struct{}{}}}})
		_ = resp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))
		_ = resp.EncodeProp(http.StatusOK, common.SupportedAddressData{
			AddressDataType: []common.AddressDataType{
				{ContentType: "text/vcard", Version: "3.0"},
				{ContentType: "text/vcard", Version: "4.0"},
			},
		})
		_ = resp.EncodeProp(http.StatusOK, struct {
			XMLName xml.Name `xml:"DAV: sync-token"`
			Text    string   `xml:",chardata"`
		}{Text: "seq:0"})
		resps = append(resps, resp)

		if depth == "1" {
			dir := c.handlers.addressbookDirs[collection]
			if dir != nil {
				contacts, err := dir.ListContacts(r.Context())
				if err != nil {
					c.handlers.logger.Error().Err(err).Str("collection", collection).Msg("failed to list LDAP contacts in PROPFIND")
				} else {
					for _, contact := range contacts {
						contactHref := common.JoinURL(c.basePath, "addressbooks", owner, collection, contact.ID+".vcf")
						contactResp := common.Response{Hrefs: []common.Href{{Value: contactHref}}}
						_ = contactResp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/vcard; charset=utf-8"})
						etag := computeStableETag(&contact)
						if etag != "" {
							_ = contactResp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(etag)})
						}
						resps = append(resps, contactResp)
					}
				}
			}
		}

		ms := common.MultiStatus{Responses: resps}
		_ = common.ServeMultiStatus(w, &ms)
		return
	}

	addressbooks, err := c.handlers.store.ListAddressbooksByOwnerUser(r.Context(), owner)
	if err != nil {
		c.handlers.logger.Error().Err(err).Str("owner", owner).Msg("failed to list addressbooks by owner in PROPFIND collection")
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	var ab *storage.Addressbook
	for _, addressbook := range addressbooks {
		if addressbook.URI == collection {
			ab = addressbook
			break
		}
	}

	if ab == nil {
		c.handlers.logger.Debug().Str("owner", owner).Str("collection", collection).Msg("collection not found in PROPFIND")
		http.NotFound(w, r)
		return
	}

	href := common.AddressbookPath(c.basePath, owner, collection)
	ownerHref := common.PrincipalURL(c.basePath, owner)
	pr := common.MustPrincipal(r.Context())

	var resps []common.Response

	propResp := common.Response{
		Hrefs: []common.Href{{Value: href}},
	}

	_ = propResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Addressbook: &struct{}{}})
	_ = propResp.EncodeProp(http.StatusOK, common.DisplayName{Name: ab.DisplayName})
	_ = propResp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: ownerHref}})
	_ = propResp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, pr.UserID)}})

	_ = propResp.EncodeProp(http.StatusOK, supportedReportSetValue())
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"DAV: sync-token"`
		Text    string   `xml:",chardata"`
	}{Text: ab.CTag})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
		Text    string   `xml:",chardata"`
	}{Text: ab.CTag})

	if !ab.UpdatedAt.IsZero() {
		_ = propResp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(ab.UpdatedAt.UTC())})
	}

	_ = propResp.EncodeProp(http.StatusOK, c.buildSupportedPrivilegeSet())
	_ = propResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
		Privilege: []common.Privilege{{All: &struct{}{}}},
	})
	_ = propResp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))

	// CardDAV capabilities
	_ = propResp.EncodeProp(http.StatusOK, common.SupportedAddressData{
		AddressDataType: []common.AddressDataType{
			{ContentType: "text/vcard", Version: "3.0"},
			{ContentType: "text/vcard", Version: "4.0"},
		},
	})
	_ = propResp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"urn:ietf:params:xml:ns:carddav max-resource-size"`
		Size    int      `xml:",chardata"`
	}{Size: c.getMaxResourceSize()})

	resps = append(resps, propResp)

	if depth == "1" {
		contacts, err := c.handlers.store.ListContacts(r.Context(), ab.ID)
		if err != nil {
			c.handlers.logger.Error().Err(err).Str("addressbook", ab.URI).Msg("failed to list contacts in PROPFIND collection")
		} else {
			for _, contact := range contacts {
				contactHref := common.JoinURL(c.basePath, "addressbooks", owner, collection, contact.UID+".vcf")
				contactResp := common.Response{Hrefs: []common.Href{{Value: contactHref}}}
				_ = contactResp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/vcard; charset=utf-8"})
				if contact.ETag != "" {
					_ = contactResp.EncodeProp(http.StatusOK, common.GetETag{ETag: common.ETag(contact.ETag)})
				}
				if !contact.UpdatedAt.IsZero() {
					_ = contactResp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(contact.UpdatedAt.UTC())})
				}
				resps = append(resps, contactResp)
			}
		}
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND collection")
	}
}

func (c *CardDAVResourceHandler) PropfindObject(w http.ResponseWriter, r *http.Request, owner, collection, object string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		c.handlers.logger.Error().Str("path", r.URL.Path).Msg("PROPFIND object unauthorized")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if strings.HasPrefix(collection, "ldap_") {
		dir := c.handlers.addressbookDirs[collection]
		if dir == nil {
			c.handlers.logger.Debug().Str("user", u.UID).Str("owner", owner).Msg("PROPFIND object forbidden - user mismatch")
			http.NotFound(w, r)
			return
		}
		uid := strings.TrimSuffix(object, filepath.Ext(object))
		_, err := dir.GetContact(r.Context(), uid)
		if err != nil {
			c.handlers.logger.Error().Err(err).Str("user", u.UID).Str("owner", owner).Msg("PROPFIND object forbidden - user mismatch")
			http.NotFound(w, r)
			return
		}
		hrefStr := common.JoinURL(c.handlers.basePath, "addressbooks", owner, collection, uid+".vcf")
		resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
		_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/vcard; charset=utf-8"})
		ms := common.MultiStatus{Responses: []common.Response{resp}}
		_ = common.ServeMultiStatus(w, &ms)
		return
	}

	if u.UID != owner {
		c.handlers.logger.Debug().Str("user", u.UID).Str("owner", owner).Msg("PROPFIND object forbidden - user mismatch")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	uid := strings.TrimSuffix(object, filepath.Ext(object))
	addressbookID, abOwner, err := c.handlers.resolveAddressbook(r.Context(), owner, collection)
	if err != nil {
		c.handlers.logger.Error().Err(err).
			Str("owner", owner).
			Str("collection", collection).
			Str("object", object).
			Msg("failed to resolve addressbook in PROPFIND object")
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Since we only allow access to own addressbooks, the owner should match
	if abOwner != owner {
		c.handlers.logger.Debug().
			Str("user", owner).
			Str("addressbook_owner", abOwner).
			Str("collection", collection).
			Msg("access denied - not owner of addressbook")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	contact, err := c.handlers.store.GetContact(r.Context(), addressbookID, uid)
	if err != nil {
		c.handlers.logger.Debug().Err(err).
			Str("addressbookID", addressbookID).
			Str("uid", uid).
			Msg("object not found in PROPFIND object")
		http.NotFound(w, r)
		return
	}

	hrefStr := common.JoinURL(c.handlers.basePath, "addressbooks", owner, collection, uid+".vcf")

	resp := common.Response{
		Hrefs: []common.Href{{Value: hrefStr}},
	}
	_ = resp.EncodeProp(http.StatusOK, common.GetContentType{Type: "text/vcard; charset=utf-8"})
	if !contact.UpdatedAt.IsZero() {
		_ = resp.EncodeProp(http.StatusOK, common.GetLastModified{LastModified: common.TimeText(contact.UpdatedAt.UTC())})
	}

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND object")
	}
}

func (c *CardDAVResourceHandler) GetHomeSetProperty(basePath, uid string) interface{} {
	return &common.Href{Value: common.AddressbookHome(basePath, uid)}
}

func (c *CardDAVResourceHandler) getMaxResourceSize() int {
	return 5 * 1024 * 1024
}

func (c *CardDAVResourceHandler) buildSupportedPrivilegeSet() common.SupportedPrivilegeSet {
	return common.SupportedPrivilegeSet{
		SupportedPrivilege: []common.SupportedPrivilege{
			{Privilege: common.Privilege{All: &struct{}{}}, Abstract: &struct{}{}, Description: "All privileges"},
			{Privilege: common.Privilege{Read: &struct{}{}}, Description: "Read privileges"},
			{Privilege: common.Privilege{Write: &struct{}{}}, Abstract: &struct{}{}, Description: "Write privileges"},
			{Privilege: common.Privilege{WriteProperties: &struct{}{}}, Description: "Write properties privilege"},
			{Privilege: common.Privilege{WriteContent: &struct{}{}}, Description: "Write content privilege"},
			{Privilege: common.Privilege{Bind: &struct{}{}}, Description: "Create new resources"},
			{Privilege: common.Privilege{Unbind: &struct{}{}}, Description: "Delete existing resources"},
			{Privilege: common.Privilege{Unlock: &struct{}{}}, Description: "Remove locks"},
			{Privilege: common.Privilege{ReadACL: &struct{}{}}, Description: "Read ACL privilege"},
			{Privilege: common.Privilege{WriteACL: &struct{}{}}, Description: "Write ACL privilege"},
			{Privilege: common.Privilege{ReadCurrentUserPrivilegeSet: &struct{}{}}, Description: "Read current user privilege set"},
		},
	}
}

func (c *CardDAVResourceHandler) buildOwnerACL(owner string) common.ACL {
	return common.ACL{
		ACE: []common.ACE{{
			Principal: common.Principal{
				Href: &common.Href{
					Value: common.PrincipalURL(c.basePath, owner),
				},
			},
			Grant: &common.Grant{
				Privilege: []common.Privilege{{All: &struct{}{}}},
			},
			Protected: &struct{}{},
		}},
	}
}

func supportedReportSetValue() interface{} {
	return &common.SupportedReportSet{
		SupportedReport: []common.SupportedReport{
			{Report: common.ReportType{AddressbookQuery: &struct{}{}}},
			{Report: common.ReportType{AddressbookMultiget: &struct{}{}}},
			{Report: common.ReportType{SyncCollection: &struct{}{}}},
		},
	}
}
