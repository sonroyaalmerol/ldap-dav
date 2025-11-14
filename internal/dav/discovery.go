package dav

import (
	"net/http"
)

func (h *Handlers) HandleWellKnown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE, PROPFIND, MKCOL, MKCALENDAR, REPORT")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, If-Match, If-None-Match, If-Modified-Since, Depth, Content-Type, Content-Range, Content-Language, Date, Content-Length, Content-Encoding")
	w.Header().Set("Access-Control-Expose-Headers", "Dav, Content-Type, Content-Range, Content-Language, Date, Content-Length, Content-Encoding, Etag, Last-Modified")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	if r.Method == http.MethodOptions {
		h.HandleOptions(w, r)
		return
	}

	// Redirect to base path per RFC 6764
	http.Redirect(w, r, h.basePath+"/", http.StatusPermanentRedirect)
}

func (h *Handlers) HandleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, PROPFIND, REPORT, GET, PUT, DELETE, MKCOL, MKCALENDAR, PROPPATCH, HEAD")
	w.WriteHeader(http.StatusOK)
}
