package dav

import (
	"encoding/xml"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

type ResourceHandler interface {
	SplitResourcePath(urlPath string) (owner, collection string, rest []string)
	PropfindHome(w http.ResponseWriter, r *http.Request, owner, depth string)
	PropfindCollection(w http.ResponseWriter, r *http.Request, owner, collection, depth string)
	PropfindObject(w http.ResponseWriter, r *http.Request, owner, collection, object string)
	GetHomeSetProperty(basePath, uid string) interface{}
}

func (h *Handlers) determineResource(urlPath string) string {
	pp := strings.TrimPrefix(urlPath, h.basePath)
	pp = strings.TrimPrefix(pp, "/")
	return strings.ToLower(strings.SplitN(pp, "/", 2)[0])
}

func (h Handlers) isPrincipalPath(p string) bool {
	pp := strings.TrimPrefix(p, h.basePath)
	return strings.HasPrefix(pp, "/principals")
}

func (h *Handlers) HandlePropfind(w http.ResponseWriter, r *http.Request) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "0"
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.logger.Error().Err(err).Msg("failed to read PROPFIND body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	if h.isPrincipalPath(r.URL.Path) {
		h.propfindPrincipal(w, r, depth, body)
		return
	}

	resourceKey := h.determineResource(r.URL.Path)
	handler, ok := h.resourceHandlers[resourceKey]
	if ok {
		h.propfindResource(w, r, depth, handler)
		return
	}

	h.propfindRoot(w, r, body)
}

func (h *Handlers) propfindResource(w http.ResponseWriter, r *http.Request, depth string, handler ResourceHandler) {
	if owner, collection, rest := handler.SplitResourcePath(r.URL.Path); owner != "" {
		if len(rest) == 0 {
			if collection == "" {
				handler.PropfindHome(w, r, owner, depth)
				return
			}
			handler.PropfindCollection(w, r, owner, collection, depth)
			return
		}
		handler.PropfindObject(w, r, owner, collection, path.Base(r.URL.Path))
		return
	}
}

func (h *Handlers) propfindPrincipal(w http.ResponseWriter, r *http.Request, _ string, _ []byte) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		h.logger.Error().Msg("unauthorized principal PROPFIND request")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	self := common.PrincipalURL(h.basePath, u.UID)

	resp := common.Response{
		Hrefs: []common.Href{{Value: self}},
	}

	if err := resp.EncodeProp(http.StatusOK, common.ResourceType{
		Collection: nil,
		Principal:  &struct{}{},
	}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode ResourceType property")
	}
	if err := resp.EncodeProp(http.StatusOK, common.DisplayName{Name: u.DisplayName}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode DisplayName property")
	}
	if err := resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{Href: &common.Href{Value: self}}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode CurrentUserPrincipal property")
	}
	if err := resp.EncodeProp(http.StatusOK, common.CalendarHomeSet{Hrefs: []common.Href{{Value: common.CalendarHome(h.basePath, u.UID)}}}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode CalendarHomeSet property")
	}
	if err := resp.EncodeProp(http.StatusOK, common.AddressBookHomeSet{Hrefs: []common.Href{{Value: common.AddressbookHome(h.basePath, u.UID)}}}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode AddressbookHomeSet property")
	}
	if err := resp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"DAV: principal-URL"`
		Href    common.Href
	}{Href: common.Href{Value: self}}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode principal-URL property")
	}

	ms := common.NewMultiStatus(resp)
	if err := common.ServeMultiStatus(w, ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for principal")
	}
}

func (h *Handlers) propfindRoot(w http.ResponseWriter, r *http.Request, _ []byte) {
	root := r.URL.Path
	resp := common.Response{
		Hrefs: []common.Href{{Value: root}},
	}
	if err := resp.EncodeProp(http.StatusOK, common.ResourceType{
		Collection: &struct{}{},
	}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode ResourceType for root")
	}
	if err := resp.EncodeProp(http.StatusOK, common.CurrentUserPrincipal{
		Href: &common.Href{Value: common.CurrentUserPrincipalHref(r.Context(), h.basePath)},
	}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode CurrentUserPrincipal for root")
	}
	if err := resp.EncodeProp(http.StatusOK, struct {
		XMLName xml.Name `xml:"DAV: principal-URL"`
		Href    common.Href
	}{Href: common.Href{Value: common.CurrentUserPrincipalHref(r.Context(), h.basePath)}}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode principal-URL for root")
	}
	if err := resp.EncodeProp(http.StatusOK, common.PrincipalCollectionSet{
		Hrefs: []common.Href{{Value: common.JoinURL(h.basePath, "principals") + "/"}},
	}); err != nil {
		h.logger.Error().Err(err).Msg("failed to encode PrincipalCollectionSet for root")
	}

	ms := common.NewMultiStatus(resp)
	if err := common.ServeMultiStatus(w, ms); err != nil {
		h.logger.Error().Err(err).Msg("failed to serve MultiStatus for root")
	}
}

func (h *Handlers) RegisterResourceHandler(key string, handler ResourceHandler) {
	h.resourceHandlers[key] = handler
}
