package common

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

