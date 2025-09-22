package common

import "encoding/xml"

const (
	NSDAV    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
	NSCS     = "http://calendarserver.org/ns/"
)

// ---------- Multistatus response (RFC 4918) ----------

type MultiStatus struct {
	XMLName xml.Name `xml:"DAV: multistatus"`
	// Add xmlns bindings if you want explicit prefixes in output
	// D is optional; Go encoder writes clark-notation namespaces.
	XmlnsD  string     `xml:"xmlns:D,attr,omitempty"`
	XmlnsC  string     `xml:"xmlns:C,attr,omitempty"`
	XmlnsCS string     `xml:"xmlns:CS,attr,omitempty"`
	Resp    []Response `xml:"response"`
}

type Response struct {
	Href  string     `xml:"href"`
	Props []PropStat `xml:"propstat"`
}

type PropStat struct {
	Prop   Prop   `xml:"prop"`
	Status string `xml:"status"`
}

// ---------- WebDAV/CalDAV properties ----------

type Prop struct {
	ResourceType           *ResourceType       `xml:"DAV: resourcetype,omitempty"`
	DisplayName            *string             `xml:"DAV: displayname,omitempty"`
	CurrentUserPrincipal   *Href               `xml:"DAV: current-user-principal>href,omitempty"`
	PrincipalURL           *Href               `xml:"DAV: principal-URL>href,omitempty"`
	PrincipalCollectionSet *Hrefs              `xml:"DAV: principal-collection-set,omitempty"`
	Owner                  *Href               `xml:"DAV: owner>href,omitempty"`
	SyncToken              *string             `xml:"DAV: sync-token,omitempty"`
	ContentType            *string             `xml:"DAV: getcontenttype,omitempty"`
	GetETag                string              `xml:"DAV: getetag,omitempty"`
	GetLastModified        string              `xml:"DAV: getlastmodified,omitempty"`
	MatchesWithinLimits    *int                `xml:"DAV: number-of-matches-within-limits,omitempty"`
	ACL                    *AclProp            `xml:"DAV: acl,omitempty"`
	QuotaAvailableBytes    *int64              `xml:"DAV: quota-available-bytes,omitempty"` // RFC 4331
	QuotaUsedBytes         *int64              `xml:"DAV: quota-used-bytes,omitempty"`      // RFC 4331
	SupportedReportSet     *SupportedReportSet `xml:"DAV: supported-report-set,omitempty"`

	// Non-standard (Apple CalendarServer)
	GetCTag *string `xml:"http://calendarserver.org/ns/ getctag,omitempty"`

	// CalDAV properties (RFC 4791)
	CalendarHomeSet               *Href                  `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set>href,omitempty"`
	SupportedCalendarComponentSet *SupportedCompSet      `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set,omitempty"`
	SupportedCalendarData         *SupportedCalData      `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-data,omitempty"`
	CalendarDescription           *string                `xml:"urn:ietf:params:xml:ns:caldav calendar-description,omitempty"`
	CalendarTimezone              *string                `xml:"urn:ietf:params:xml:ns:caldav calendar-timezone,omitempty"`
	MaxResourceSize               *int                   `xml:"urn:ietf:params:xml:ns:caldav max-resource-size,omitempty"`
	MinDateTime                   *string                `xml:"urn:ietf:params:xml:ns:caldav min-date-time,omitempty"`
	MaxDateTime                   *string                `xml:"urn:ietf:params:xml:ns:caldav max-date-time,omitempty"`
	MaxInstances                  *int                   `xml:"urn:ietf:params:xml:ns:caldav max-instances,omitempty"`
	MaxAttendeesPerInstance       *int                   `xml:"urn:ietf:params:xml:ns:caldav max-attendees-per-instance,omitempty"`
	SupportedCollationSet         *SupportedCollationSet `xml:"urn:ietf:params:xml:ns:caldav supported-collation-set,omitempty"`

	// Inline calendar data payload (when returning calendar-data in a REPORT)
	CalendarDataText string `xml:"urn:ietf:params:xml:ns:caldav calendar-data,omitempty"`
}

type ResourceType struct {
	Collection *struct{} `xml:"DAV: collection,omitempty"`
	Principal  *struct{} `xml:"DAV: principal,omitempty"`
	Calendar   *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar,omitempty"`
}

type Href struct {
	Value string `xml:",chardata"`
}
type Hrefs struct {
	Values []string `xml:"DAV: href"`
}

type SupportedCalData struct {
	ContentType string `xml:"content-type,attr"`
	Version     string `xml:"version,attr,omitempty"`
}

type SupportedCollationSet struct {
	SupportedCollation []SupportedCollation `xml:"urn:ietf:params:xml:ns:caldav supported-collation"`
}
type SupportedCollation struct {
	Value string `xml:",chardata"`
}

type SupportedCompSet struct {
	Comp []Comp `xml:"urn:ietf:params:xml:ns:caldav comp"`
}
type Comp struct {
	Name string `xml:"name,attr"`
}

// ---------- ACL ----------

type AclProp struct {
	ACE []Ace `xml:"DAV: ace"`
}
type Ace struct {
	Principal Href  `xml:"DAV: principal>href"`
	Grant     Grant `xml:"DAV: grant"`
}
type Grant struct {
	Privs []Priv `xml:"DAV: privilege"`
}
type Priv struct {
	Read         *struct{} `xml:"DAV: read,omitempty"`
	WriteProps   *struct{} `xml:"DAV: write-properties,omitempty"`
	WriteContent *struct{} `xml:"DAV: write-content,omitempty"`
	Bind         *struct{} `xml:"DAV: bind,omitempty"`
	Unbind       *struct{} `xml:"DAV: unbind,omitempty"`
}

// ---------- Supported reports (RFC 4918 + RFC 4791 + RFC 6578) ----------

type SupportedReportSet struct {
	SupportedReport []SupportedReport `xml:"DAV: supported-report"`
}
type SupportedReport struct {
	Report ReportType `xml:"DAV: report"`
}
type ReportType struct {
	CalendarQuery    *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar-query,omitempty"`
	CalendarMultiget *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget,omitempty"`
	FreeBusyQuery    *struct{} `xml:"urn:ietf:params:xml:ns:caldav free-busy-query,omitempty"`
	SyncCollection   *struct{} `xml:"DAV: sync-collection,omitempty"` // RFC 6578
}

// ---------- Request bodies (REPORT and PROPFIND support) ----------

type PropRequest struct {
	GetETag      bool
	CalendarData bool
}

// PropContainer collects requested properties under DAV:prop
type PropContainer struct {
	XMLName xml.Name `xml:"DAV: prop"`
	// Any holds namespaced property names (use carefully if you add dynamic props)
	Any []xml.Name
}

// CalDAV calendar-query REPORT
type CalendarQuery struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:caldav calendar-query"`
	XmlnsD  string         `xml:"xmlns:D,attr,omitempty"`
	XmlnsC  string         `xml:"xmlns:C,attr,omitempty"`
	Prop    PropContainer  `xml:"DAV: prop"`
	Filter  CalendarFilter `xml:"urn:ietf:params:xml:ns:caldav filter"`
}

// CalDAV calendar-multiget REPORT
type CalendarMultiget struct {
	XMLName xml.Name      `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget"`
	XmlnsD  string        `xml:"xmlns:D,attr,omitempty"`
	XmlnsC  string        `xml:"xmlns:C,attr,omitempty"`
	Prop    PropContainer `xml:"DAV: prop"`
	Hrefs   []string      `xml:"DAV: href"`
}

// WebDAV sync-collection REPORT (RFC 6578)
type SyncCollection struct {
	XMLName   xml.Name   `xml:"DAV: sync-collection"`
	XmlnsD    string     `xml:"xmlns:D,attr,omitempty"`
	SyncToken string     `xml:"DAV: sync-token"`
	Limit     *SyncLimit `xml:"DAV: limit,omitempty"`
}
type SyncLimit struct {
	NResults int `xml:"DAV: nresults"`
}

// Calendar filter grammar (RFC 4791)
type CalendarFilter struct {
	CompFilter CompFilter `xml:"urn:ietf:params:xml:ns:caldav comp-filter"`
}
type CompFilter struct {
	Name       string      `xml:"name,attr"`
	CompFilter *CompFilter `xml:"urn:ietf:params:xml:ns:caldav comp-filter,omitempty"`
	TimeRange  *TimeRange  `xml:"urn:ietf:params:xml:ns:caldav time-range,omitempty"`
}
type TimeRange struct {
	Start string `xml:"start,attr,omitempty"`
	End   string `xml:"end,attr,omitempty"`
}

// CalDAV free-busy-query REPORT
type FreeBusyQuery struct {
	XMLName xml.Name   `xml:"urn:ietf:params:xml:ns:caldav free-busy-query"`
	XmlnsC  string     `xml:"xmlns:C,attr,omitempty"`
	Time    *TimeRange `xml:"urn:ietf:params:xml:ns:caldav time-range"`
}
