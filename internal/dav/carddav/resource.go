package carddav

import (
	"encoding/xml"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
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
	visible, err := c.handlers.aclProv.VisibleCalendars(r.Context(), u)
	if err != nil {
		c.handlers.logger.Error().Err(err).Str("user", u.UID).Msg("failed to compute visible addressbooks in PROPFIND home")
		http.Error(w, "acl error", http.StatusInternalServerError)
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

		u, _ := common.CurrentUser(r.Context())
		if u != nil {
			ldapAddressbooks, err := c.handlers.dir.ListAddressbooks(r.Context(), &directory.User{UID: u.UID, DN: u.DN, DisplayName: u.DisplayName})
			if err != nil {
				c.handlers.logger.Error().Err(err).Str("user", u.UID).Msg("failed to list LDAP addressbooks")
			} else {
				for _, ldapAB := range ldapAddressbooks {
					if !ldapAB.Enabled {
						continue
					}

					hrefStr := common.AddressbookPath(c.basePath, owner, ldapAB.ID)
					resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
					_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Addressbook: &struct{}{}})
					_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: ldapAB.Name})
					_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
					_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})
					_ = resp.EncodeProp(http.StatusOK, supportedReportSetValue())

					_ = resp.EncodeProp(http.StatusOK, struct {
						XMLName xml.Name `xml:"DAV: sync-token"`
						Text    string   `xml:",chardata"`
					}{Text: "ldap-readonly"})
					_ = resp.EncodeProp(http.StatusOK, struct {
						XMLName xml.Name `xml:"http://calendarserver.org/ns/ getctag"`
						Text    string   `xml:",chardata"`
					}{Text: "ldap-readonly"})

					_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
						Privilege: []common.Privilege{{Read: &struct{}{}}},
					})

					_ = resp.EncodeProp(http.StatusOK, c.buildOwnerACL(owner))
					resps = append(resps, resp)
				}
			}
		}

		sharedBase := common.AddressbookSharedRoot(c.basePath, owner)
		sharedResp := common.Response{Hrefs: []common.Href{{Value: sharedBase}}}
		_ = sharedResp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
		_ = sharedResp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Shared"})
		_ = sharedResp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}})

		sharedEffectivePrivileges := acl.Effective{
			Read:                        true,
			ReadCurrentUserPrivilegeSet: true,
		}

		if sharedEffectivePrivileges.CanReadCurrentUserPrivilegeSet() {
			_ = sharedResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
				Privilege: []common.Privilege{{Read: &struct{}{}}},
			})
		}

		resps = append(resps, sharedResp)

		all, err := c.handlers.store.ListAllAddressbooks(r.Context())
		if err != nil {
			c.handlers.logger.Error().Err(err).Msg("failed to list all addressbooks in PROPFIND home")
		} else {
			for _, ab := range all {
				if ab.OwnerUserID == owner {
					continue
				}
				if eff, aok := visible[ab.URI]; aok && eff.CanRead() {
					hrefStr := common.JoinURL(sharedBase, ab.URI) + "/"
					resp := common.Response{Hrefs: []common.Href{{Value: hrefStr}}}
					_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}, Addressbook: &struct{}{}})
					_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: ab.DisplayName})
					_ = resp.EncodeProp(http.StatusOK, common.Owner{Href: &common.Href{Value: c.ownerPrincipalForAddressbook(ab)}})
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

					if eff.CanReadCurrentUserPrivilegeSet() {
						privs := c.effectiveToPrivileges(eff)
						_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{Privilege: privs})
					}

					if eff.CanReadACL() {
						acl := c.buildSharedACL(ab.OwnerUserID, owner, eff)
						_ = resp.EncodeProp(http.StatusOK, acl)
					}
					resps = append(resps, resp)
				}
			}
		}
	}

	ms := common.MultiStatus{Responses: resps}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus in PROPFIND home")
	}
}

func (c *CardDAVResourceHandler) PropfindCollection(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	requesterUID := owner

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

	var trueOwner string
	isSharedMount := false
	if ab == nil && collection != "shared" {
		if sab, err := c.handlers.findAddressbookByURI(r.Context(), collection); err == nil && sab != nil {
			pr := common.MustPrincipal(r.Context())
			if ok, err := c.handlers.aclCheckRead(r.Context(), pr, sab.URI, sab.OwnerUserID); err == nil && ok {
				ab = sab
				trueOwner = sab.OwnerUserID
				isSharedMount = true
			} else if err != nil {
				c.handlers.logger.Error().Err(err).
					Str("addressbook", sab.URI).
					Str("owner", sab.OwnerUserID).
					Msg("ACL check failed in PROPFIND collection reading shared addressbook")
			}
		}
	}

	if ab == nil && collection == "shared" {
		resp := common.Response{
			Hrefs: []common.Href{{Value: common.JoinURL(c.basePath, "addressbooks", owner, "shared") + "/"}},
		}
		_ = resp.EncodeProp(http.StatusOK, common.ResourceType{Collection: &struct{}{}})
		_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Shared"})

		pr := common.MustPrincipal(r.Context())
		_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, pr.UserID)}})

		_ = resp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{
			Privilege: []common.Privilege{{Read: &struct{}{}}},
		})

		ms := common.MultiStatus{Responses: []common.Response{resp}}
		if err := common.ServeMultiStatus(w, &ms); err != nil {
			c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND shared collection")
		}
		return
	}

	if ab == nil {
		c.handlers.logger.Debug().Str("owner", owner).Str("collection", collection).Msg("collection not found in PROPFIND")
		http.NotFound(w, r)
		return
	}

	var href string
	var ownerHref string
	if isSharedMount {
		href = common.JoinURL(common.AddressbookSharedRoot(c.basePath, requesterUID), collection) + "/"
		ownerHref = common.PrincipalURL(c.basePath, trueOwner)
	} else {
		href = common.AddressbookPath(c.basePath, owner, collection)
		ownerHref = common.PrincipalURL(c.basePath, owner)
	}

	pr := common.MustPrincipal(r.Context())

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

	// ACL exposure
	if isSharedMount && trueOwner != "" && pr.UserID != trueOwner {
		if eff, err := c.handlers.aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, collection); err == nil {
			if eff.CanReadCurrentUserPrivilegeSet() {
				currentUserPrivs := c.effectiveToPrivileges(eff)
				_ = propResp.EncodeProp(http.StatusOK, common.CurrentUserPrivilegeSet{Privilege: currentUserPrivs})
			}
			if eff.CanReadACL() {
				acl := c.buildCollectionACL(trueOwner, pr.UserID, isSharedMount, eff)
				_ = propResp.EncodeProp(http.StatusOK, acl)
			}
		}
	} else {
		acl := c.buildOwnerACL(pr.UserID)
		_ = propResp.EncodeProp(http.StatusOK, acl)
	}

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

	ms := common.MultiStatus{Responses: []common.Response{propResp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for PROPFIND collection")
	}
}

func (c *CardDAVResourceHandler) PropfindObject(w http.ResponseWriter, r *http.Request, owner, collection, object string) {
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
	pr := common.MustPrincipal(r.Context())
	okRead, err := c.handlers.aclCheckRead(r.Context(), pr, collection, abOwner)
	if err != nil || !okRead {
		c.handlers.logger.Debug().Err(err).
			Bool("can_read", okRead).
			Str("user", pr.UserID).
			Str("collection", collection).
			Msg("ACL check failed or denied in PROPFIND object")
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

func (c *CardDAVResourceHandler) ownerPrincipalForAddressbook(ab *storage.Addressbook) string {
	if ab.OwnerUserID != "" {
		return common.PrincipalURL(c.basePath, ab.OwnerUserID)
	}
	return common.JoinURL(c.basePath, "principals")
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

func (c *CardDAVResourceHandler) effectiveToPrivileges(eff acl.Effective) []common.Privilege {
	var privs []common.Privilege
	if eff.CanRead() {
		privs = append(privs, common.Privilege{Read: &struct{}{}})
	}
	if eff.WriteProps && eff.WriteContent && eff.CanCreate() && eff.CanDelete() {
		privs = append(privs, common.Privilege{Write: &struct{}{}})
	} else {
		if eff.WriteProps {
			privs = append(privs, common.Privilege{WriteProperties: &struct{}{}})
		}
		if eff.WriteContent {
			privs = append(privs, common.Privilege{WriteContent: &struct{}{}})
		}
		if eff.CanCreate() {
			privs = append(privs, common.Privilege{Bind: &struct{}{}})
		}
		if eff.CanDelete() {
			privs = append(privs, common.Privilege{Unbind: &struct{}{}})
		}
	}
	if eff.CanUnlock() {
		privs = append(privs, common.Privilege{Unlock: &struct{}{}})
	}
	if eff.CanReadACL() {
		privs = append(privs, common.Privilege{ReadACL: &struct{}{}})
	}
	if eff.CanWriteACL() {
		privs = append(privs, common.Privilege{WriteACL: &struct{}{}})
	}
	privs = append(privs, common.Privilege{ReadCurrentUserPrivilegeSet: &struct{}{}})
	return privs
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

func (c *CardDAVResourceHandler) buildSharedACL(trueOwner, requester string, eff acl.Effective) common.ACL {
	var aces []common.ACE

	aces = append(aces, common.ACE{
		Principal: common.Principal{
			Href: &common.Href{
				Value: common.PrincipalURL(c.basePath, trueOwner),
			},
		},
		Grant: &common.Grant{
			Privilege: []common.Privilege{{All: &struct{}{}}},
		},
		Protected: &struct{}{},
	})

	if requester != trueOwner {
		reqPrivs := c.effectiveToPrivileges(eff)
		if len(reqPrivs) > 0 {
			aces = append(aces, common.ACE{
				Principal: common.Principal{
					Href: &common.Href{
						Value: common.PrincipalURL(c.basePath, requester),
					},
				},
				Grant: &common.Grant{
					Privilege: reqPrivs,
				},
			})
		}
	}

	return common.ACL{ACE: aces}
}

func (c *CardDAVResourceHandler) buildCollectionACL(trueOwner, requesterID string, isSharedMount bool, eff acl.Effective) common.ACL {
	var aces []common.ACE

	ownerPrincipalURL := common.PrincipalURL(c.basePath, trueOwner)
	if trueOwner == "" {
		ownerPrincipalURL = common.PrincipalURL(c.basePath, requesterID)
	}

	aces = append(aces, common.ACE{
		Principal: common.Principal{
			Href: &common.Href{Value: ownerPrincipalURL},
		},
		Grant: &common.Grant{
			Privilege: []common.Privilege{{All: &struct{}{}}},
		},
		Protected: &struct{}{},
	})

	if isSharedMount && trueOwner != "" && requesterID != trueOwner {
		reqPrivs := c.effectiveToPrivileges(eff)
		if len(reqPrivs) > 0 {
			aces = append(aces, common.ACE{
				Principal: common.Principal{
					Href: &common.Href{
						Value: common.PrincipalURL(c.basePath, requesterID),
					},
				},
				Grant: &common.Grant{
					Privilege: reqPrivs,
				},
			})
		}
	}

	return common.ACL{ACE: aces}
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
