package router

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/caldav"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/carddav"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

var _ DAVService = (*caldav.Handlers)(nil)
var _ DAVService = (*carddav.Handlers)(nil)

func New(cfg *config.Config, h *dav.Handlers, authn *auth.Chain, logger zerolog.Logger) http.Handler {
	r := &Router{
		config:   cfg,
		handlers: h,
		auth:     authn,
		logger:   logger,
		services: make(map[string]DAVService),
	}

	r.RegisterService("caldav", &h.CalDAVHandlers)
	r.RegisterService("carddav", &h.CardDAVHandlers)

	return r.setupRoutes()
}

func (r *Router) RegisterService(name string, service DAVService) {
	r.services[name] = service
}

func (r *Router) setupRoutes() http.Handler {
	mux := http.NewServeMux()

	r.setupWellKnownRoutes(mux)

	mux.HandleFunc("/healthz", r.handleHealth)

	base := r.getBasePath()
	mux.HandleFunc(base, r.handleDAVRequest)

	if strings.HasSuffix(base, "/") {
		baseWithoutSlash := strings.TrimSuffix(base, "/")
		mux.HandleFunc(baseWithoutSlash, r.handleDAVRequest)
	}

	return mux
}

func (r *Router) setupWellKnownRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/caldav", r.handlers.HandleWellKnown)
	mux.HandleFunc("/.well-known/carddav", r.handlers.HandleWellKnown)
}

func (r *Router) getBasePath() string {
	base := r.config.HTTP.BasePath
	if base == "" || base[0] != '/' {
		base = "/dav"
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (r *Router) handleDAVRequest(w http.ResponseWriter, req *http.Request) {
	capabilities := r.buildDAVCapabilities()
	w.Header().Set("DAV", capabilities)

	// OPTIONS is public for capability discovery
	if req.Method == http.MethodOptions {
		r.handlers.HandleOptions(w, req)
		return
	}

	p, err := r.authenticate(req)
	if err != nil || p == nil {
		r.logAttempt(req, "", err)
		w.Header().Set("WWW-Authenticate", `Basic realm="DAV", charset="UTF-8"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	req = req.WithContext(auth.WithPrincipal(req.Context(), p))

	r.routeDAVMethod(w, req)
}

func (r *Router) buildDAVCapabilities() string {
	baseCapabilities := []string{"1", "3", "access-control"}

	// Add service-specific capabilities
	for _, service := range r.services {
		if caps := service.GetCapabilities(); caps != "" {
			baseCapabilities = append(baseCapabilities, caps)
		}
	}

	return strings.Join(baseCapabilities, ", ")
}

func (r *Router) routeDAVMethod(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: 0, wroteHeader: false}

	ip := realIP(req)
	method := req.Method
	path := req.URL.Path
	ua := req.Header.Get("User-Agent")

	user, _ := common.CurrentUser(req.Context())

	// Determine service type based on path or content-type
	serviceName := r.determineServiceType(req)
	service, exists := r.services[serviceName]

	if !exists {
		// Fallback to CalDAV for backward compatibility
		service = r.services["caldav"]
	}

	switch req.Method {
	case "PROPFIND":
		r.handlers.HandlePropfind(rec, req)
	case "REPORT":
		service.HandleReport(rec, req)
	case http.MethodGet:
		service.HandleGet(rec, req)
	case http.MethodHead:
		service.HandleHead(rec, req)
	case http.MethodPut:
		service.HandlePut(rec, req)
	case http.MethodDelete:
		service.HandleDelete(rec, req)
	case "MKCOL":
		service.HandleMkcol(rec, req)
	case "MKCALENDAR":
		service.HandleMkcalendar(rec, req)
	case "PROPPATCH":
		service.HandleProppatch(rec, req)
	default:
		http.Error(rec, "method not allowed", http.StatusMethodNotAllowed)
	}

	dur := time.Since(start)

	var logEvent *zerolog.Event
	switch req.Method {
	case "PROPFIND", "REPORT", http.MethodGet, http.MethodHead:
		logEvent = r.logger.Debug()
	default:
		logEvent = r.logger.Info()
	}

	logEntry := logEvent.
		Str("method", method).
		Str("path", path).
		Str("service", serviceName).
		Int("status", statusOrDefault(rec.status)).
		Int("bytes", rec.bytes).
		Float64("duration_ms", float64(dur.Microseconds())/1000.0).
		Str("ip", ip).
		Str("user_agent", ua)

	if user != nil {
		logEntry = logEntry.Str("user", user.UID)
	}

	logEntry.Msg("http request")
}

func (r *Router) determineServiceType(req *http.Request) string {
	if strings.Contains(req.URL.Path, "/calendars/") || strings.Contains(req.URL.Path, "/calendar/") {
		return "caldav"
	}
	if strings.Contains(req.URL.Path, "/addressbooks/") || strings.Contains(req.URL.Path, "/contacts/") {
		return "carddav"
	}

	// Check content type for PUT requests
	if req.Method == http.MethodPut {
		contentType := req.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/calendar") || strings.Contains(contentType, "text/vcard") {
			if strings.Contains(contentType, "text/vcard") {
				return "carddav"
			}
			return "caldav"
		}
	}

	// Default to caldav for backward compatibility
	return "caldav"
}

func (r *Router) authenticate(req *http.Request) (*auth.Principal, error) {
	authz := req.Header.Get("Authorization")
	lower := strings.ToLower(authz)

	// Prefer Bearer if present and enabled
	if strings.HasPrefix(lower, "bearer ") && r.auth.BearerEnabled() {
		return r.auth.BearerAuthenticate(req.Context(), strings.TrimSpace(authz[7:]))
	}

	// Basic when header present or allowed for prompt
	if r.auth.BasicEnabled() {
		return r.auth.BasicAuthenticate(req.Context(), authz)
	}

	return nil, errors.New("no auth")
}

func (r *Router) logAttempt(req *http.Request, username string, authErr error) {
	ip := realIP(req)
	ua := req.Header.Get("User-Agent")
	authz := req.Header.Get("Authorization")
	authType := ""
	if i := strings.IndexByte(authz, ' '); i > 0 {
		authType = strings.ToLower(authz[:i])
	}

	logEvent := r.logger.Info().
		Bool("auth_success", false).
		Str("user", username).
		Str("method", req.Method).
		Str("path", req.URL.Path).
		Str("ip", ip).
		Str("user_agent", ua).
		Str("auth_type", authType)

	if authErr != nil {
		logEvent = logEvent.Str("error", authErr.Error())
	}

	logEvent.Msg("auth attempt")
}
