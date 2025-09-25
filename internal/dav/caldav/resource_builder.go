package caldav

import (
	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func (c *CalDAVResourceHandler) buildSupportedPrivilegeSet() common.SupportedPrivilegeSet {
	return common.SupportedPrivilegeSet{
		SupportedPrivilege: []common.SupportedPrivilege{
			{Privilege: common.Privilege{All: &struct{}{}}, Description: "All privileges"},
			{Privilege: common.Privilege{Read: &struct{}{}}, Description: "Read privileges"},
			{Privilege: common.Privilege{Write: &struct{}{}}, Description: "Write privileges"},
			{Privilege: common.Privilege{WriteProperties: &struct{}{}}, Description: "Write properties privilege"},
			{Privilege: common.Privilege{WriteContent: &struct{}{}}, Description: "Write content privilege"},
			{Privilege: common.Privilege{Bind: &struct{}{}}, Description: "Create new resources"},
			{Privilege: common.Privilege{Unbind: &struct{}{}}, Description: "Delete existing resources"},
			{Privilege: common.Privilege{Unlock: &struct{}{}}, Description: "Remove locks"},
			{Privilege: common.Privilege{ReadACL: &struct{}{}}, Description: "Read ACL privilege"},
			{Privilege: common.Privilege{WriteACL: &struct{}{}}, Description: "Write ACL privilege"},
			{Privilege: common.Privilege{ReadCurrentUserPrivilegeSet: &struct{}{}}, Description: "Read current user privilege set"},
			{Privilege: common.Privilege{ReadFreeBusy: &struct{}{}}, Description: "Read free-busy information"},
		},
	}
}

func (c *CalDAVResourceHandler) effectiveToPrivileges(eff acl.Effective) []common.Privilege {
	var privs []common.Privilege

	if eff.CanRead() {
		privs = append(privs,
			common.Privilege{Read: &struct{}{}},
			common.Privilege{ReadFreeBusy: &struct{}{}},
		)
	}

	if eff.WriteProps && eff.WriteContent {
		privs = append(privs, common.Privilege{Write: &struct{}{}})
	} else {
		if eff.WriteProps {
			privs = append(privs, common.Privilege{WriteProperties: &struct{}{}})
		}

		if eff.WriteContent {
			privs = append(privs, common.Privilege{WriteContent: &struct{}{}})
		}
	}

	if eff.CanCreate() {
		privs = append(privs, common.Privilege{Bind: &struct{}{}})
	}

	if eff.CanDelete() {
		privs = append(privs, common.Privilege{Unbind: &struct{}{}})
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

func (c *CalDAVResourceHandler) buildOwnerACL(owner string) common.ACL {
	return common.ACL{
		ACE: []common.ACE{{
			Principal: common.Principal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, owner)}},
			Grant: &common.Grant{
				Privilege: []common.Privilege{{All: &struct{}{}}},
			},
			Protected: &struct{}{},
		}},
	}
}

func (c *CalDAVResourceHandler) buildSharedACL(trueOwner, requester string, eff acl.Effective) common.ACL {
	var aces []common.ACE

	aces = append(aces, common.ACE{
		Principal: common.Principal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, trueOwner)}},
		Grant: &common.Grant{
			Privilege: []common.Privilege{{All: &struct{}{}}},
		},
		Protected: &struct{}{},
	})

	if requester != trueOwner {
		aces = append(aces, common.ACE{
			Principal: common.Principal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, requester)}},
			Grant: &common.Grant{
				Privilege: c.effectiveToPrivileges(eff),
			},
		})
	}

	return common.ACL{ACE: aces}
}

func (c *CalDAVResourceHandler) buildCollectionACL(trueOwner, requesterID string, isSharedMount bool, eff acl.Effective) common.ACL {
	var aces []common.ACE

	ownerPrincipalURL := common.PrincipalURL(c.basePath, trueOwner)
	if trueOwner == "" {
		ownerPrincipalURL = common.PrincipalURL(c.basePath, requesterID)
	}

	aces = append(aces, common.ACE{
		Principal: common.Principal{Href: &common.Href{Value: ownerPrincipalURL}},
		Grant: &common.Grant{
			Privilege: []common.Privilege{{All: &struct{}{}}},
		},
		Protected: &struct{}{},
	})

	if isSharedMount && trueOwner != "" && requesterID != trueOwner {
		aces = append(aces, common.ACE{
			Principal: common.Principal{Href: &common.Href{Value: common.PrincipalURL(c.basePath, requesterID)}},
			Grant: &common.Grant{
				Privilege: c.effectiveToPrivileges(eff),
			},
		})
	}

	return common.ACL{ACE: aces}
}
