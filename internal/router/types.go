package router

import (
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/auth"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/dav"
)

type DAVService interface {
	GetCapabilities() string
	HandleGet(w http.ResponseWriter, r *http.Request)
	HandleHead(w http.ResponseWriter, r *http.Request)
	HandlePut(w http.ResponseWriter, r *http.Request)
	HandleDelete(w http.ResponseWriter, r *http.Request)
	HandleMkcol(w http.ResponseWriter, r *http.Request)
	HandleMkcalendar(w http.ResponseWriter, r *http.Request)
	HandleProppatch(w http.ResponseWriter, r *http.Request)
	HandleReport(w http.ResponseWriter, r *http.Request)
	HandleACL(w http.ResponseWriter, r *http.Request)
}

type Router struct {
	config   *config.Config
	handlers *dav.Handlers
	auth     *auth.Chain
	logger   interface{}

	services map[string]DAVService
}
