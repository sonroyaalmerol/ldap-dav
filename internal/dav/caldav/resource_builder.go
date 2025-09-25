package caldav

import (
	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func (c *CalDAVResourceHandler) buildSupportedPrivilegeSet() common.SupportedPrivilegeSet {
	return common.SupportedPrivilegeSet{
		SupportedPrivilege: []common.SupportedPrivilege{
			// DAV:all - abstract privilege containing all others
			{
				Privilege:   common.Privilege{All: &struct{}{}},
				Abstract:    &struct{}{},
				Description: "All privileges",
			},
			// DAV:read - atomic privilege
			{
				Privilege:   common.Privilege{Read: &struct{}{}},
				Description: "Read privileges",
			},
			// DAV:write - abstract privilege (RFC 3744 requires it contains 4 sub-privileges)
			{
				Privilege:   common.Privilege{Write: &struct{}{}},
				Abstract:    &struct{}{},
				Description: "Write privileges",
			},
			// Required sub-privileges of DAV:write
			{
				Privilege:   common.Privilege{WriteProperties: &struct{}{}},
				Description: "Write properties privilege",
			},
			{
				Privilege:   common.Privilege{WriteContent: &struct{}{}},
				Description: "Write content privilege",
			},
			{
				Privilege:   common.Privilege{Bind: &struct{}{}},
				Description: "Create new resources",
			},
			{
				Privilege:   common.Privilege{Unbind: &struct{}{}},
				Description: "Delete existing resources",
			},
			// Other atomic privileges
			{
				Privilege:   common.Privilege{Unlock: &struct{}{}},
				Description: "Remove locks",
			},
			{
				Privilege:   common.Privilege{ReadACL: &struct{}{}},
				Description: "Read ACL privilege",
			},
			{
				Privilege:   common.Privilege{WriteACL: &struct{}{}},
				Description: "Write ACL privilege",
			},
			{
				Privilege:   common.Privilege{ReadCurrentUserPrivilegeSet: &struct{}{}},
				Description: "Read current user privilege set",
			},
			// CalDAV-specific privilege
			{
				Privilege:   common.Privilege{ReadFreeBusy: &struct{}{}},
				Description: "Read free-busy information",
			},
		},
	}
}

func (c *CalDAVResourceHandler) effectiveToPrivileges(eff acl.Effective) []common.Privilege {
	var privs []common.Privilege

	// RFC 3744: DAV:read MUST NOT contain DAV:write, DAV:write-acl,
	// DAV:write-properties, or DAV:write-content
	if eff.CanRead() {
		privs = append(privs, common.Privilege{Read: &struct{}{}})
		// CalDAV-specific read privilege
		privs = append(privs, common.Privilege{ReadFreeBusy: &struct{}{}})
	}

	// RFC 3744: DAV:write MUST contain DAV:bind, DAV:unbind,
	// DAV:write-properties and DAV:write-content
	// So we can only grant DAV:write if ALL four are available
	if eff.WriteProps && eff.WriteContent && eff.CanCreate() && eff.CanDelete() {
		privs = append(privs, common.Privilege{Write: &struct{}{}})
	} else {
		// Grant individual write privileges if not all are available
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

	// RFC 3744: DAV:read-acl MUST NOT contain DAV:read, DAV:write,
	// DAV:write-acl, DAV:write-properties, DAV:write-content,
	// or DAV:read-current-user-privilege-set
	if eff.CanReadACL() {
		privs = append(privs, common.Privilege{ReadACL: &struct{}{}})
	}

	// RFC 3744: DAV:write-acl MUST NOT contain DAV:write, DAV:read,
	// DAV:read-acl, or DAV:read-current-user-privilege-set
	if eff.CanWriteACL() {
		privs = append(privs, common.Privilege{WriteACL: &struct{}{}})
	}

	// RFC 3744: DAV:read-current-user-privilege-set MUST NOT contain
	// DAV:write, DAV:read, DAV:read-acl, or DAV:write-acl
	privs = append(privs, common.Privilege{ReadCurrentUserPrivilegeSet: &struct{}{}})

	return privs
}

// Owner gets DAV:all which contains everything
func (c *CalDAVResourceHandler) buildOwnerACL(owner string) common.ACL {
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

func (c *CalDAVResourceHandler) buildSharedACL(trueOwner, requester string, eff acl.Effective) common.ACL {
	var aces []common.ACE

	// Owner always gets DAV:all
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

	// Requester gets limited privileges based on effective permissions
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

func (c *CalDAVResourceHandler) buildCollectionACL(trueOwner, requesterID string, isSharedMount bool, eff acl.Effective) common.ACL {
	var aces []common.ACE

	ownerPrincipalURL := common.PrincipalURL(c.basePath, trueOwner)
	if trueOwner == "" {
		ownerPrincipalURL = common.PrincipalURL(c.basePath, requesterID)
	}

	// Owner (or requester if no owner) gets DAV:all
	aces = append(aces, common.ACE{
		Principal: common.Principal{
			Href: &common.Href{Value: ownerPrincipalURL},
		},
		Grant: &common.Grant{
			Privilege: []common.Privilege{{All: &struct{}{}}},
		},
		Protected: &struct{}{},
	})

	// For shared mounts, add requester's limited privileges
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
