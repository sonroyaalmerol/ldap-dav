package dav

import (
	"encoding/xml"
	"io"
	"net/http"

	"github.com/sonroyaalmerol/ldap-dav/internal/dav/common"
)

func (h *Handlers) HandleReport(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	_ = r.Body.Close()

	root := struct {
		XMLName xml.Name
	}{}
	if err := xml.Unmarshal(body, &root); err != nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}

	switch root.XMLName.Space + " " + root.XMLName.Local {
	case common.NSCalDAV + " calendar-query":
		var q common.CalendarQuery
		_ = xml.Unmarshal(body, &q)
		h.CalDAVHandlers.ReportCalendarQuery(w, r, q)
	case common.NSCalDAV + " calendar-multiget":
		var mg common.CalendarMultiget
		_ = xml.Unmarshal(body, &mg)
		h.CalDAVHandlers.ReportCalendarMultiget(w, r, mg)
	case common.NSDAV + " sync-collection":
		var sc common.SyncCollection
		_ = xml.Unmarshal(body, &sc)
		h.CalDAVHandlers.ReportSyncCollection(w, r, sc)
	case common.NSCalDAV + " free-busy-query":
		var fb common.FreeBusyQuery
		_ = xml.Unmarshal(body, &fb)
		h.CalDAVHandlers.ReportFreeBusyQuery(w, r, fb)
	default:
		http.Error(w, "unsupported REPORT", http.StatusBadRequest)
	}
}
