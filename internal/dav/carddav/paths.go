package carddav

import (
	"strings"
)

// tryAddressbookShorthand interprets /addressbooks/{abURI} as /addressbooks/{currentUserID}/{abURI}
func tryAddressbookShorthand(urlPath, basePath, currentUserID string) (owner, abURI string, ok bool) {
	pp := urlPath
	// Accept both absolute and full-URL hrefs (mirror splitResourcePath logic)
	if !strings.HasPrefix(pp, "/") {
		if strings.HasPrefix(pp, "http://") || strings.HasPrefix(pp, "https://") {
			if idx := strings.Index(pp, "://"); idx >= 0 {
				if slash := strings.Index(pp[idx+3:], "/"); slash >= 0 {
					pp = pp[idx+3+slash:]
				}
			}
		}
	}
	pp = strings.TrimPrefix(pp, basePath)
	pp = strings.TrimPrefix(pp, "/")
	parts := strings.Split(pp, "/")
	if len(parts) == 2 && parts[0] == "addressbooks" {
		return currentUserID, parts[1], true
	}
	return "", "", false
}

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
	// addressbooks/{owner}/ -> home
	// addressbooks/{owner}/{ab}/...
	if len(parts) == 0 {
		return "", "", nil
	}
	if parts[0] != "addressbooks" {
		return "", "", nil
	}
	if len(parts) == 2 {
		// /addressbooks/{owner}/ (home)
		return parts[1], "", nil
	}

	// Shared normalization:
	// /addressbooks/{owner}/shared/{targetAbURI}/...
	if len(parts) >= 4 && parts[2] == "shared" {
		return parts[1], parts[3], parts[4:]
	}

	if len(parts) >= 3 {
		// Owned addressbook
		return parts[1], parts[2], parts[3:]
	}
	return "", "", nil
}
