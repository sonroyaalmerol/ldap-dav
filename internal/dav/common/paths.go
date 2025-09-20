package common

import "strings"

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

