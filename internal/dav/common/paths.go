package common

import (
	"context"
	"strings"
)

func PrincipalURL(basePath, uid string) string {
	return JoinURL(basePath, "principals", "users", uid)
}

func JoinURL(parts ...string) string {
	s := strings.Join(parts, "/")
	s = strings.ReplaceAll(s, "//", "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

func CalendarHome(basePath, uid string) string {
	return JoinURL(basePath, "calendars", uid) + "/"
}

func CalendarPath(basePath, ownerUID, calURI string) string {
	return JoinURL(basePath, "calendars", ownerUID, calURI) + "/"
}

func CalendarSharedRoot(basePath, uid string) string {
	return JoinURL(basePath, "calendars", uid, "shared") + "/"
}

func CurrentUserPrincipalHref(ctx context.Context, basePath string) string {
	u, _ := CurrentUser(ctx)
	if u == nil {
		return JoinURL(basePath, "principals")
	}
	return PrincipalURL(basePath, u.UID)
}
