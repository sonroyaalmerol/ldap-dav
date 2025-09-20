package common

import (
	"context"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
)

func MustPrincipal(ctx context.Context) *auth.Principal {
	pr, _ := auth.PrincipalFrom(ctx)
	return pr
}

func CurrentUser(ctx context.Context) (*directory.User, *auth.Principal) {
	pr, ok := auth.PrincipalFrom(ctx)
	if !ok || pr == nil {
		return nil, nil
	}
	return &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, pr
}
