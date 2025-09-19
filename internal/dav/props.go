package dav

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

const (
	nsDAV    = "DAV:"
	nsCalDAV = "urn:ietf:params:xml:ns:caldav"
)

type multistatus struct {
	XMLName xml.Name   `xml:"DAV: multistatus"`
	Resp    []response `xml:"response"`
}

type supportedCalData struct {
	ContentType string `xml:"content-type,attr"`
	Version     string `xml:"version,attr,omitempty"`
}

type response struct {
	Href  string     `xml:"href"`
	Prop  propstat   `xml:"propstat"`
	Extra []propstat `xml:"propstat,omitempty"`
}

type propstat struct {
	Prop   prop   `xml:"prop"`
	Status string `xml:"status"`
}

type prop struct {
	Resourcetype                  *resourcetype     `xml:"resourcetype,omitempty"`
	DisplayName                   *string           `xml:"displayname,omitempty"`
	CurrentUserPrincipal          *href             `xml:"current-user-principal>href,omitempty"`
	PrincipalURL                  *href             `xml:"principal-URL>href,omitempty"`
	PrincipalCollectionSet        *hrefs            `xml:"principal-collection-set>href,omitempty"`
	CalendarHomeSet               *href             `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set>href,omitempty"`
	SupportedCalendarComponentSet *supportedCompSet `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set,omitempty"`
	Owner                         *href             `xml:"owner>href,omitempty"`
	GetCTag                       *string           `xml:"http://calendarserver.org/ns/ getctag,omitempty"`
	SyncToken                     *string           `xml:"DAV: sync-token,omitempty"`
	ContentType                   *string           `xml:"getcontenttype,omitempty"`
	CalendarDataText              string            `xml:"urn:ietf:params:xml:ns:caldav calendar-data,omitempty"`
	GetETag                       string            `xml:"getetag,omitempty"`
	GetLastModified               string            `xml:"getlastmodified,omitempty"`
	MatchesWithinLimits           *int              `xml:"DAV: number-of-matches-within-limits,omitempty"`
	SupportedCalendarData         *supportedCalData `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-data,omitempty"`
	ACL                           *aclProp          `xml:"DAV: acl,omitempty"`
}

type aclProp struct {
	// Minimal representation: a single ACE showing effective privileges for current user
	ACE []ace `xml:"ace"`
}
type ace struct {
	Principal href  `xml:"principal>href"`
	Grant     grant `xml:"grant"`
}
type grant struct {
	Privs []priv `xml:"privilege"`
}
type priv struct {
	// Represent common privileges; we’ll use <read/>, <write-properties/>, <write-content/>, <bind/>, <unbind/>
	Read         *struct{} `xml:"read,omitempty"`
	WriteProps   *struct{} `xml:"write-properties,omitempty"`
	WriteContent *struct{} `xml:"write-content,omitempty"`
	Bind         *struct{} `xml:"bind,omitempty"`
	Unbind       *struct{} `xml:"unbind,omitempty"`
}

type resourcetype struct {
	Collection *struct{} `xml:"collection,omitempty"`
	Principal  *struct{} `xml:"principal,omitempty"`
	Calendar   *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar,omitempty"`
}

type href struct {
	Value string `xml:",chardata"`
}
type hrefs struct {
	Values []string `xml:"href"`
}

type supportedCompSet struct {
	Comp []comp `xml:"comp"`
}
type comp struct {
	Name string `xml:"name,attr"`
}

func writeMultiStatus(w http.ResponseWriter, ms multistatus) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(ms); err != nil {
		http.Error(w, fmt.Sprintf("xml encode error: %v", err), http.StatusInternalServerError)
		return
	}
	_ = enc.Flush()
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	_, _ = w.Write(buf.Bytes())
}

func ok() string { return "HTTP/1.1 200 OK" }

func makeCalendarResourcetype() *resourcetype {
	return &resourcetype{
		Collection: &struct{}{},
		Calendar:   &struct{}{},
	}
}
func makeCollectionResourcetype() *resourcetype {
	return &resourcetype{
		Collection: &struct{}{},
	}
}
func makePrincipalResourcetype() *resourcetype {
	return &resourcetype{
		Principal:  &struct{}{},
		Collection: nil,
	}
}

func calContentType() *string {
	ct := "text/calendar; charset=utf-8"
	return &ct
}

func supportedVEVENT() *supportedCompSet {
	return &supportedCompSet{Comp: []comp{{Name: "VEVENT"}}}
}

func joinURL(parts ...string) string {
	s := strings.Join(parts, "/")
	s = strings.ReplaceAll(s, "//", "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

func (h *Handlers) currentUserPrincipalHref(ctx context.Context) string {
	u, _ := h.currentUser(ctx)
	if u == nil {
		return joinURL(h.basePath, "principals")
	}
	return h.principalURL(u.UID)
}

func isPrincipalUsers(p, base string) bool {
	pp := strings.TrimPrefix(p, base)
	return strings.HasPrefix(pp, "/principals")
}

func strPtr(s string) *string { return &s }
