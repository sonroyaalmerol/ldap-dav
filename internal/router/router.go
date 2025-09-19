package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"
)

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
		sr.Options("/*", h.HandleOptions)
		sr.Route("/principals", func(pr chi.Router) {
			pr.Handle("/*", h)
		})
		sr.Route("/calendars", func(cr chi.Router) {
			cr.Handle("/*", h)
		})
		sr.Handle("/*", h)
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return r
}
