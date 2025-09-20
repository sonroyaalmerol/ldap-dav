package router

import (
	"errors"
	"net/http"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func New(cfg *config.Config, h *dav.Handlers, authn *auth.Chain, logger interface{}) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/caldav", h.HandleWellKnown)

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	base := cfg.HTTP.BasePath
	if base == "" || base[0] != '/' {
		base = "/dav"
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	// DAV subtree
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		// Always advertise DAV capabilities under DAV subtree
		w.Header().Set("DAV", "1, 3, access-control, calendar-access")

		// OPTIONS is public for capability discovery
		if r.Method == http.MethodOptions {
			h.HandleOptions(w, r)
			return
		}

		// Authenticate others (Basic/Bearer). Allow missing Authorization only if Basic is enabled (for browser prompt).
		p, err := authenticate(authn, r)
		if err != nil || p == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="CalDAV", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(auth.WithPrincipal(r.Context(), p))

		switch r.Method {
		case "PROPFIND":
			h.HandlePropfind(w, r)
		case "REPORT":
			h.HandleReport(w, r)
		case http.MethodGet:
			if common.IsCalendarPath(r.URL.Path, base) {
				h.CalDAVHandlers.HandleGet(w, r)
			}
		case http.MethodHead:
			if common.IsCalendarPath(r.URL.Path, base) {
				hrw := &headResponseWriter{ResponseWriter: w}
				h.CalDAVHandlers.HandleGet(hrw, r)
			}
		case http.MethodPut:
			if common.IsCalendarPath(r.URL.Path, base) {
				h.CalDAVHandlers.HandlePut(w, r)
			}
		case http.MethodDelete:
			if common.IsCalendarPath(r.URL.Path, base) {
				h.CalDAVHandlers.HandleDelete(w, r)
			}
		case "MKCOL":
			if common.IsCalendarPath(r.URL.Path, base) {
				h.CalDAVHandlers.HandleMkcol(w, r)
			}
		case "PROPPATCH":
			if common.IsCalendarPath(r.URL.Path, base) {
				h.CalDAVHandlers.HandleProppatch(w, r)
			}
		case "ACL":
			if common.IsCalendarPath(r.URL.Path, base) {
				h.CalDAVHandlers.HandleACL(w, r)
			}
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	return mux
}

// authenticate reproduces the Chain.Middleware logic without writing to the ResponseWriter.
func authenticate(chain *auth.Chain, r *http.Request) (*auth.Principal, error) {
	authz := r.Header.Get("Authorization")
	lower := strings.ToLower(authz)

	// Prefer Bearer if present and enabled
	if strings.HasPrefix(lower, "bearer ") && chain.BearerEnabled() {
		return chain.BearerAuthenticate(r.Context(), strings.TrimSpace(authz[7:]))
	}

	// Basic when header present or allowed for prompt
	if chain.BasicEnabled() {
		return chain.BasicAuthenticate(r.Context(), authz)
	}

	return nil, errors.New("no auth")
}
