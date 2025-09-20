package caldav

import (
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func splitResourcePath(urlPath, basePath string) (owner, collection string, rest []string) {
	// Accept both absolute and full-URL hrefs
	if !strings.HasPrefix(urlPath, "/") {
		if strings.HasPrefix(urlPath, "http://") || strings.HasPrefix(urlPath, "https://") {
			// HREF may be full URL; extract path
			if idx := strings.Index(urlPath, "://"); idx >= 0 {
				if slash := strings.Index(urlPath[idx+3:], "/"); slash >= 0 {
					urlPath = urlPath[idx+3+slash:]
				}
			}
		}
	}
	pp := urlPath
	pp = strings.TrimPrefix(pp, basePath)
	pp = strings.TrimPrefix(pp, "/")
	parts := strings.Split(pp, "/")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	// patterns:
	// calendars/{owner}/ -> home
	// calendars/{owner}/{cal}/...
	if len(parts) == 0 {
		return "", "", nil
	}
	if parts[0] != "calendars" {
		return "", "", nil
	}
	if len(parts) == 2 {
		// /calendars/{owner}/ (home)
		return parts[1], "", nil
	}

	// Shared normalization:
	// /calendars/{owner}/shared/{targetCalURI}/...
	if len(parts) >= 4 && parts[2] == "shared" {
		return parts[1], parts[3], parts[4:]
	}

	if len(parts) >= 3 {
		// Owned calendar
		return parts[1], parts[2], parts[3:]
	}
	return "", "", nil
}

func calendarHome(basePath, uid string) string {
	return common.JoinURL(basePath, "calendars", uid) + "/"
}

func calendarPath(basePath, ownerUID, calURI string) string {
	return common.JoinURL(basePath, "calendars", ownerUID, calURI) + "/"
}

func sharedRoot(basePath, uid string) string {
	return common.JoinURL(basePath, "calendars", uid, "shared") + "/"
}
