package common

import (
	"context"
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
)

func BuildReadOnlyACL(r *http.Request, basePath, calURI, ownerUID string, aclProv acl.Provider) *AclProp {
	pr := MustPrincipal(r.Context())
	if pr == nil {
		return nil
	}

	isOwner := pr.UserID == ownerUID
	var eff acl.Effective

	if isOwner {
		eff = acl.Effective{
			Read:                        true,
			WriteProps:                  true,
			WriteContent:                true,
			Bind:                        true,
			Unbind:                      true,
			Unlock:                      true,
			ReadACL:                     true,
			ReadCurrentUserPrivilegeSet: true,
			WriteACL:                    false,
		}
	} else {
		e, err := aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			return nil
		}
		eff = e
	}

	var privs []Priv
	p := Priv{}

	if eff.Read {
		p.Read = &struct{}{}
	}
	if eff.WriteProps && eff.WriteContent {
		p.Write = &struct{}{}
	}
	if eff.WriteProps {
		p.WriteProps = &struct{}{}
	}
	if eff.WriteContent {
		p.WriteContent = &struct{}{}
	}
	if eff.Bind {
		p.Bind = &struct{}{}
	}
	if eff.Unbind {
		p.Unbind = &struct{}{}
	}
	if eff.Unlock {
		p.Unlock = &struct{}{}
	}
	if eff.ReadACL {
		p.ReadACL = &struct{}{}
	}
	if eff.ReadCurrentUserPrivilegeSet {
		p.ReadCurrentUserPrivilegeSet = &struct{}{}
	}
	if eff.WriteACL {
		p.WriteACL = &struct{}{}
	}

	if p.Read != nil || p.WriteProps != nil || p.WriteContent != nil || p.Bind != nil ||
		p.Unbind != nil || p.Unlock != nil || p.ReadACL != nil ||
		p.ReadCurrentUserPrivilegeSet != nil || p.WriteACL != nil {
		privs = append(privs, p)
	}

	return &AclProp{
		ACE: []Ace{
			{
				Principal: Href{Value: PrincipalURL(basePath, pr.UserID)},
				Grant:     Grant{Privs: privs},
			},
		},
	}
}

func CurrentUserPrincipalHref(ctx context.Context, basePath string) string {
	u, _ := CurrentUser(ctx)
	if u == nil {
		return JoinURL(basePath, "principals")
	}
	return PrincipalURL(basePath, u.UID)
}
