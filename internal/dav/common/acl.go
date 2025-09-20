package common

import (
	"context"
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func BuildReadOnlyACL(r *http.Request, basePath, calURI, ownerUID string, aclProv acl.Provider) *AclProp {
	pr := MustPrincipal(r.Context())
	if pr == nil {
		return nil
	}

	isOwner := pr.UserID == ownerUID
	var eff struct{ Read, WP, WC, B, U bool }
	if isOwner {
		eff = struct{ Read, WP, WC, B, U bool }{true, true, true, true, true}
	} else {
		e, err := aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			return nil
		}
		eff = struct{ Read, WP, WC, B, U bool }{e.CanRead(), e.WriteProps, e.WriteContent, e.Bind, e.Unbind}
	}
	g := Grant{}
	p := Priv{}
	if eff.Read {
		p.Read = &struct{}{}
	}
	if eff.WP {
		p.WriteProps = &struct{}{}
	}
	if eff.WC {
		p.WriteContent = &struct{}{}
	}
	if eff.B {
		p.Bind = &struct{}{}
	}
	if eff.U {
		p.Unbind = &struct{}{}
	}
	if p.Read != nil || p.WriteProps != nil || p.WriteContent != nil || p.Bind != nil || p.Unbind != nil {
		g.Privs = append(g.Privs, p)
	}
	return &AclProp{
		ACE: []Ace{
			{
				Principal: Href{Value: PrincipalURL(basePath, pr.UserID)},
				Grant:     g,
			},
		},
	}
}

func OwnerPrincipalForCalendar(c *storage.Calendar, basePath string) string {
	if c.OwnerUserID != "" {
		return PrincipalURL(basePath, c.OwnerUserID)
	}
	// could be group-owned; expose group principal path if implemented
	return JoinURL(basePath, "principals")
}

func CurrentUserPrincipalHref(ctx context.Context, basePath string) string {
	u, _ := CurrentUser(ctx)
	if u == nil {
		return JoinURL(basePath, "principals")
	}
	return PrincipalURL(basePath, u.UID)
}
