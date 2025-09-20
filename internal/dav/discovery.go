package dav

import (
	"net/http"
)

func (h *Handlers) HandleWellKnown(w http.ResponseWriter, r *http.Request) {
	// Redirect to base path per RFC 6764
	http.Redirect(w, r, h.basePath+"/", http.StatusPermanentRedirect)
}

func (h *Handlers) HandleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, PROPFIND, REPORT, GET, PUT, DELETE, MKCOL, PROPPATCH, ACL, HEAD")
	w.WriteHeader(http.StatusOK)
}
