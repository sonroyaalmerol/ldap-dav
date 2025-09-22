package dav

import (
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

	_, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()

	if h.isPrincipalPath(r.URL.Path) {
		h.propfindPrincipal(w, r, depth)
		return
	}

	resourceKey := h.determineResource(r.URL.Path)
	handler, ok := h.resourceHandlers[resourceKey]
	if ok {
		h.propfindResource(w, r, depth, handler)
		return
	}

	h.propfindRoot(w, r)
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

func (h *Handlers) propfindPrincipal(w http.ResponseWriter, r *http.Request, depth string) {
	u, _ := common.CurrentUser(r.Context())
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	self := common.PrincipalURL(h.basePath, u.UID)
	prop := common.Prop{
		ResourceType:         common.MakePrincipalResourcetype(),
		DisplayName:          &u.DisplayName,
		PrincipalURL:         &common.Href{Value: self},
		CurrentUserPrincipal: &common.Href{Value: self},
	}

	for _, handler := range h.resourceHandlers {
		if homeSet := handler.GetHomeSetProperty(h.basePath, u.UID); homeSet != nil {
			switch hs := homeSet.(type) {
			case *common.Href:
				if prop.CalendarHomeSet == nil {
					prop.CalendarHomeSet = hs
				}
			}
		}
	}

	ms := common.MultiStatus{
		Resp: []common.Response{
			{
				Href: self,
				Props: []common.PropStat{{
					Prop:   prop,
					Status: common.Ok(),
				}},
			},
		},
	}
	common.WriteMultiStatus(w, ms)
}

func (h *Handlers) propfindRoot(w http.ResponseWriter, r *http.Request) {
	href := r.URL.Path
	if !strings.HasSuffix(href, "/") {
		href += "/"
	}

	ms := common.MultiStatus{
		Resp: []common.Response{
			{
				Href: href,
				Props: []common.PropStat{{
					Prop: common.Prop{
						ResourceType:           common.MakeCollectionResourcetype(),
						CurrentUserPrincipal:   &common.Href{Value: common.CurrentUserPrincipalHref(r.Context(), h.basePath)},
						PrincipalURL:           &common.Href{Value: common.CurrentUserPrincipalHref(r.Context(), h.basePath)},
						PrincipalCollectionSet: &common.Hrefs{Values: []string{common.JoinURL(h.basePath, "principals") + "/"}},
					},
					Status: common.Ok(),
				}},
			},
		},
	}
	common.WriteMultiStatus(w, ms)
}

func (h *Handlers) RegisterResourceHandler(key string, handler ResourceHandler) {
	h.resourceHandlers[key] = handler
}
