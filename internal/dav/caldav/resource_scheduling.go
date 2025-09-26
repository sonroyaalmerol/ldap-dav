package caldav

import (
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func (c *CalDAVResourceHandler) propfindSchedulingInbox(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	_, err := c.handlers.store.GetSchedulingInbox(r.Context(), owner)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	href := common.JoinURL(c.basePath, "calendars", owner, collection) + "/"

	resp := common.Response{
		Hrefs: []common.Href{{Value: href}},
	}

	_ = resp.EncodeProp(http.StatusOK, common.ResourceType{
		Collection:    &struct{}{},
		ScheduleInbox: &struct{}{},
	})
	_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Scheduling Inbox"})

	// Add scheduling-specific properties
	_ = resp.EncodeProp(http.StatusOK, common.CalendarFreeBusySet{
		Hrefs: []common.Href{{Value: common.CalendarHome(c.basePath, owner)}},
	})

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for scheduling inbox")
	}
}

func (c *CalDAVResourceHandler) propfindSchedulingOutbox(w http.ResponseWriter, r *http.Request, owner, collection, depth string) {
	pr := common.MustPrincipal(r.Context())
	if pr.UserID != owner {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	_, err := c.handlers.store.GetSchedulingOutbox(r.Context(), owner)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	href := common.JoinURL(c.basePath, "calendars", owner, collection) + "/"

	resp := common.Response{
		Hrefs: []common.Href{{Value: href}},
	}

	_ = resp.EncodeProp(http.StatusOK, common.ResourceType{
		Collection:     &struct{}{},
		ScheduleOutbox: &struct{}{},
	})
	_ = resp.EncodeProp(http.StatusOK, common.DisplayName{Name: "Scheduling Outbox"})

	// Add scheduling-specific properties
	_ = resp.EncodeProp(http.StatusOK, common.CalendarFreeBusySet{
		Hrefs: []common.Href{{Value: common.CalendarHome(c.basePath, owner)}},
	})

	ms := common.MultiStatus{Responses: []common.Response{resp}}
	if err := common.ServeMultiStatus(w, &ms); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to serve MultiStatus for scheduling outbox")
	}
}
