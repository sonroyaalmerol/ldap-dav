package router

import (
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"

	"github.com/go-chi/chi/v5"
)

func New(cfg *config.Config, h *dav.Handlers, authn *auth.Chain, logger interface{}) http.Handler {
	r := chi.NewRouter()

	r.Get("/.well-known/caldav", h.HandleWellKnownCalDAV)

	r.Route(cfg.HTTP.BasePath, func(r chi.Router) {
		r.Options("/*", h.HandleOptions)
		r.Use(authn.Middleware)

		r.Handle("/principals/*", h)
		r.Handle("/calendars/*", h)
		r.Handle("/*", h)
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	return r
}
