package common

import "encoding/xml"

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

type PropRequest struct {
	GetETag      bool
	CalendarData bool
}

type PropContainer struct {
	XMLName xml.Name `xml:"DAV: prop"`
	Any     []xml.Name
}

type CalendarQuery struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:caldav calendar-query"`
	Prop    PropContainer  `xml:"DAV: prop"`
	Filter  CalendarFilter `xml:"filter"`
}

type CalendarMultiget struct {
	XMLName xml.Name      `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget"`
	Prop    PropContainer `xml:"DAV: prop"`
	Hrefs   []string      `xml:"DAV: href"`
}

type SyncCollection struct {
	XMLName   xml.Name   `xml:"DAV: sync-collection"`
	SyncToken string     `xml:"sync-token"`
	Limit     *SyncLimit `xml:"limit,omitempty"`
}

type SyncLimit struct {
	NResults int `xml:"nresults"`
}

type CalendarFilter struct {
	CompFilter CompFilter `xml:"comp-filter"`
}
type CompFilter struct {
	Name       string      `xml:"name,attr"`
	CompFilter *CompFilter `xml:"comp-filter,omitempty"`
	TimeRange  *TimeRange  `xml:"time-range,omitempty"`
}
type TimeRange struct {
	Start string `xml:"start,attr,omitempty"`
	End   string `xml:"end,attr,omitempty"`
}

// free-busy REPORT
type FreeBusyQuery struct {
	XMLName xml.Name   `xml:"urn:ietf:params:xml:ns:caldav free-busy-query"`
	Time    *TimeRange `xml:"time-range"`
}
