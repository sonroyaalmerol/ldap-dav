package common

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/acl"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

const (
	NSDAV    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
)

type MultiStatus struct {
	XMLName xml.Name   `xml:"DAV: multistatus"`
	Resp    []Response `xml:"response"`
}

type SupportedCalData struct {
	ContentType string `xml:"content-type,attr"`
	Version     string `xml:"version,attr,omitempty"`
}

type Response struct {
	Href  string     `xml:"href"`
	Props []PropStat `xml:"propstat"`
}

type PropStat struct {
	Prop   Prop   `xml:"prop"`
	Status string `xml:"status"`
}

type Prop struct {
	ResourceType                  *ResourceType     `xml:"resourcetype,omitempty"`
	DisplayName                   *string           `xml:"displayname,omitempty"`
	CurrentUserPrincipal          *Href             `xml:"current-user-principal>href,omitempty"`
	PrincipalURL                  *Href             `xml:"principal-URL>href,omitempty"`
	PrincipalCollectionSet        *Hrefs            `xml:"principal-collection-set>href,omitempty"`
	CalendarHomeSet               *Href             `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set>href,omitempty"`
	SupportedCalendarComponentSet *SupportedCompSet `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set,omitempty"`
	Owner                         *Href             `xml:"owner>href,omitempty"`
	GetCTag                       *string           `xml:"http://calendarserver.org/ns/ getctag,omitempty"`
	SyncToken                     *string           `xml:"DAV: sync-token,omitempty"`
	ContentType                   *string           `xml:"getcontenttype,omitempty"`
	CalendarDataText              string            `xml:"urn:ietf:params:xml:ns:caldav calendar-data,omitempty"`
	GetETag                       string            `xml:"getetag,omitempty"`
	GetLastModified               string            `xml:"getlastmodified,omitempty"`
	MatchesWithinLimits           *int              `xml:"DAV: number-of-matches-within-limits,omitempty"`
	SupportedCalendarData         *SupportedCalData `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-data,omitempty"`
	ACL                           *AclProp          `xml:"DAV: acl,omitempty"`
}

type AclProp struct {
	// Minimal representation: a single ACE showing effective privileges for current user
	ACE []Ace `xml:"ace"`
}
type Ace struct {
	Principal Href  `xml:"principal>href"`
	Grant     Grant `xml:"grant"`
}
type Grant struct {
	Privs []Priv `xml:"privilege"`
}
type Priv struct {
	// Represent common privileges; we’ll use <read/>, <write-properties/>, <write-content/>, <bind/>, <unbind/>
	Read         *struct{} `xml:"read,omitempty"`
	WriteProps   *struct{} `xml:"write-properties,omitempty"`
	WriteContent *struct{} `xml:"write-content,omitempty"`
	Bind         *struct{} `xml:"bind,omitempty"`
	Unbind       *struct{} `xml:"unbind,omitempty"`
}

type ResourceType struct {
	Collection *struct{} `xml:"collection,omitempty"`
	Principal  *struct{} `xml:"principal,omitempty"`
	Calendar   *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar,omitempty"`
}

type Href struct {
	Value string `xml:",chardata"`
}
type Hrefs struct {
	Values []string `xml:"href"`
}

type SupportedCompSet struct {
	Comp []Comp `xml:"comp"`
}
type Comp struct {
	Name string `xml:"name,attr"`
}

func WriteMultiStatus(w http.ResponseWriter, ms MultiStatus) {
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

func Ok() string { return "HTTP/1.1 200 OK" }

func MakeCalendarResourcetype() *ResourceType {
	return &ResourceType{
		Collection: &struct{}{},
		Calendar:   &struct{}{},
	}
}
func MakeCollectionResourcetype() *ResourceType {
	return &ResourceType{
		Collection: &struct{}{},
	}
}
func MakePrincipalResourcetype() *ResourceType {
	return &ResourceType{
		Principal:  &struct{}{},
		Collection: nil,
	}
}

func CalContentType() *string {
	ct := "text/calendar; charset=utf-8"
	return &ct
}

func JoinURL(parts ...string) string {
	s := strings.Join(parts, "/")
	s = strings.ReplaceAll(s, "//", "/")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

func IsPrincipalUsers(p, base string) bool {
	pp := strings.TrimPrefix(p, base)
	return strings.HasPrefix(pp, "/principals")
}

func StrPtr(s string) *string { return &s }

func BuildReadOnlyACL(r *http.Request, basePath, calURI, ownerUID string, aclProv acl.Provider) *AclProp {
	pr := MustPrincipal(r.Context())
	if pr == nil {
		return nil
	}

	isOwner := pr.UserID == ownerUID
	var eff struct{ Read, WP, WC, B, U bool }
	if isOwner {
		eff = struct{ Read, WP, WC, B, U bool }{true, true, true, true, true}
	} else {
		e, err := aclProv.Effective(r.Context(), &directory.User{UID: pr.UserID, DN: pr.UserDN, DisplayName: pr.Display}, calURI)
		if err != nil {
			return nil
		}
		eff = struct{ Read, WP, WC, B, U bool }{e.CanRead(), e.WriteProps, e.WriteContent, e.Bind, e.Unbind}
	}
	g := Grant{}
	p := Priv{}
	if eff.Read {
		p.Read = &struct{}{}
	}
	if eff.WP {
		p.WriteProps = &struct{}{}
	}
	if eff.WC {
		p.WriteContent = &struct{}{}
	}
	if eff.B {
		p.Bind = &struct{}{}
	}
	if eff.U {
		p.Unbind = &struct{}{}
	}
	if p.Read != nil || p.WriteProps != nil || p.WriteContent != nil || p.Bind != nil || p.Unbind != nil {
		g.Privs = append(g.Privs, p)
	}
	return &AclProp{
		ACE: []Ace{
			{
				Principal: Href{Value: PrincipalURL(basePath, pr.UserID)},
				Grant:     g,
			},
		},
	}
}

func OwnerPrincipalForCalendar(c *storage.Calendar, basePath string) string {
	if c.OwnerUserID != "" {
		return PrincipalURL(basePath, c.OwnerUserID)
	}
	// could be group-owned; expose group principal path if implemented
	return JoinURL(basePath, "principals")
}
