package dav

import (
	"net/http"
)

func (h *Handlers) HandleWellKnownCalDAV(w http.ResponseWriter, r *http.Request) {
	// Redirect to base path per RFC 6764
	http.Redirect(w, r, h.basePath+"/", http.StatusPermanentRedirect)
}

func (h *Handlers) HandleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("DAV", "1, 3, access-control, calendar-access")
	w.Header().Set("Allow", "OPTIONS, PROPFIND, REPORT, GET, PUT, DELETE, MKCOL, PROPPATCH, ACL")
	w.WriteHeader(http.StatusOK)
}
