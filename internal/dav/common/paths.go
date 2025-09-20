package common

import "strings"

func PrincipalURL(basePath, uid string) string {
	return JoinURL(basePath, "principals", "users", uid)
}

func CalendarHome(basePath, uid string) string {
	return JoinURL(basePath, "calendars", uid) + "/"
}

func CalendarPath(basePath, ownerUID, calURI string) string {
	return JoinURL(basePath, "calendars", ownerUID, calURI) + "/"
}

func SharedRoot(basePath, uid string) string {
	return JoinURL(basePath, "calendars", uid, "shared") + "/"
}

func JoinURL(parts ...string) string {
	s := strings.Join(parts, "/")
	s = strings.ReplaceAll(s, "//", "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

func IsPrincipalUsers(p, base string) bool {
	pp := strings.TrimPrefix(p, base)
	return strings.HasPrefix(pp, "/principals")
}

func IsCalendarUsers(p, base string) bool {
	pp := strings.TrimPrefix(p, base)
	return strings.HasPrefix(pp, "/calendars")
}

func IsAddressBookUsers(p, base string) bool {
	pp := strings.TrimPrefix(p, base)
	return strings.HasPrefix(pp, "/addressbooks")
}
