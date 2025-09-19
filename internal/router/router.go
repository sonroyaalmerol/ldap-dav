package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"
)

func handlerWrapper(handler func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return http.HandlerFunc(handler)
}

func headHandler(handler func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &headResponseWriter{ResponseWriter: w}
		handler(rw, r)
	})
}

func New(cfg *config.Config, h *dav.Handlers, authn *auth.Chain, logger interface{}) http.Handler {
	r := chi.NewRouter()

	r.Get("/.well-known/caldav", h.HandleWellKnownCalDAV)

	r.Route(cfg.HTTP.BasePath, func(sr chi.Router) {
		sr.Use(authn.Middleware)
		sr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Advertise capabilities for any DAV subtree request
				w.Header().Set("DAV", "1, 3, access-control, calendar-access")
				next.ServeHTTP(w, r)
			})
		})

		// OPTIONS for any DAV path
		sr.MethodFunc("OPTIONS", "/*", h.HandleOptions)

		// Principals subtree
		sr.Route("/principals", func(pr chi.Router) {
			pr.MethodFunc("PROPFIND", "/*", handlerWrapper(h.HandlePropfind))
			pr.MethodFunc("REPORT", "/*", handlerWrapper(h.HandleReport))
			pr.MethodFunc("GET", "/*", handlerWrapper(h.HandleGet))
			pr.MethodFunc("HEAD", "/*", headHandler(h.HandleGet))
			pr.MethodFunc("PUT", "/*", handlerWrapper(h.HandlePut))
			pr.MethodFunc("DELETE", "/*", handlerWrapper(h.HandleDelete))
			pr.MethodFunc("MKCOL", "/*", handlerWrapper(h.HandleMkcol))
			pr.MethodFunc("PROPPATCH", "/*", handlerWrapper(h.HandleProppatch))
			pr.MethodFunc("ACL", "/*", handlerWrapper(h.HandleACL))
		})

		// Calendars subtree
		sr.Route("/calendars", func(pr chi.Router) {
			pr.MethodFunc("PROPFIND", "/*", handlerWrapper(h.HandlePropfind))
			pr.MethodFunc("REPORT", "/*", handlerWrapper(h.HandleReport))
			pr.MethodFunc("GET", "/*", handlerWrapper(h.HandleGet))
			pr.MethodFunc("HEAD", "/*", headHandler(h.HandleGet))
			pr.MethodFunc("PUT", "/*", handlerWrapper(h.HandlePut))
			pr.MethodFunc("DELETE", "/*", handlerWrapper(h.HandleDelete))
			pr.MethodFunc("MKCOL", "/*", handlerWrapper(h.HandleMkcol))
			pr.MethodFunc("PROPPATCH", "/*", handlerWrapper(h.HandleProppatch))
			pr.MethodFunc("ACL", "/*", handlerWrapper(h.HandleACL))
		})

		sr.MethodFunc("PROPFIND", "/*", handlerWrapper(h.HandlePropfind))
		sr.MethodFunc("REPORT", "/*", handlerWrapper(h.HandleReport))
		sr.MethodFunc("GET", "/*", handlerWrapper(h.HandleGet))
		sr.MethodFunc("HEAD", "/*", headHandler(h.HandleGet))
		sr.MethodFunc("PUT", "/*", handlerWrapper(h.HandlePut))
		sr.MethodFunc("DELETE", "/*", handlerWrapper(h.HandleDelete))
		sr.MethodFunc("MKCOL", "/*", handlerWrapper(h.HandleMkcol))
		sr.MethodFunc("PROPPATCH", "/*", handlerWrapper(h.HandleProppatch))
		sr.MethodFunc("ACL", "/*", handlerWrapper(h.HandleACL))
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return r
}
