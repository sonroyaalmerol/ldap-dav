package common

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	NSDAV    = "DAV:"
	NSCalDAV = "urn:ietf:params:xml:ns:caldav"
	NSCS     = "http://calendarserver.org/ns/"
)

type Status struct {
	Code int
	Text string
}

func (s *Status) MarshalText() ([]byte, error) {
	text := s.Text
	if text == "" {
		text = http.StatusText(s.Code)
	}
	return []byte(fmt.Sprintf("HTTP/1.1 %v %v", s.Code, text)), nil
}

func (s *Status) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		return nil
	}

	parts := strings.SplitN(string(b), " ", 3)
	if len(parts) != 3 {
		return fmt.Errorf("webdav: invalid HTTP status %q: expected 3 fields", string(b))
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("webdav: invalid HTTP status %q: failed to parse code: %v", string(b), err)
	}

	s.Code = code
	s.Text = parts[2]
	return nil
}

func (s *Status) Err() error {
	if s == nil {
		return nil
	}
	if s.Code/100 == 2 {
		return nil
	}
	return &HTTPError{Code: s.Code}
}

type HTTPError struct {
	Code int
	Err  error
}

func (he *HTTPError) Error() string {
	if he.Err != nil {
		return he.Err.Error()
	}
	if t := http.StatusText(he.Code); t != "" {
		return t
	}
	return fmt.Sprintf("HTTP %d", he.Code)
}

func HTTPErrorf(code int, format string, a ...interface{}) *HTTPError {
	return &HTTPError{Code: code, Err: fmt.Errorf(format, a...)}
}

func HTTPErrorFromError(err error) *HTTPError {
	var h *HTTPError
	if errors.As(err, &h) {
		return h
	}
	return &HTTPError{Code: http.StatusInternalServerError, Err: err}
}

type Href struct {
	Value string `xml:",chardata"`
}

type MultiStatus struct {
	XMLName                     xml.Name   `xml:"DAV: multistatus"`
	Responses                   []Response `xml:"response"`
	ResponseDescription         string     `xml:"responsedescription,omitempty"`
	SyncToken                   string     `xml:"sync-token,omitempty"`
	NumberOfMatchesWithinLimits string     `xml:"number-of-matches-within-limits,omitempty"`
}

func NewMultiStatus(resps ...Response) *MultiStatus {
	return &MultiStatus{Responses: resps}
}

type Response struct {
	XMLName             xml.Name   `xml:"DAV: response"`
	Hrefs               []Href     `xml:"href"`
	PropStats           []PropStat `xml:"propstat,omitempty"`
	ResponseDescription string     `xml:"responsedescription,omitempty"`
	Status              *Status    `xml:"status,omitempty"`
	Error               *Error     `xml:"error,omitempty"`
	Location            *Location  `xml:"location,omitempty"`
}

func (resp *Response) EncodeProp(code int, v interface{}) error {
	raw, err := EncodeRawXMLElement(v)
	if err != nil {
		return err
	}
	for i := range resp.PropStats {
		if resp.PropStats[i].Status.Code == code {
			resp.PropStats[i].Prop.Raw = append(resp.PropStats[i].Prop.Raw, *raw)
			return nil
		}
	}
	resp.PropStats = append(resp.PropStats, PropStat{
		Prop:   Prop{Raw: []RawXMLValue{*raw}},
		Status: Status{Code: code},
	})
	return nil
}

type Location struct {
	XMLName xml.Name `xml:"DAV: location"`
	Href    Href     `xml:"href"`
}

type PropStat struct {
	XMLName             xml.Name `xml:"DAV: propstat"`
	Prop                Prop     `xml:"prop"`
	Status              Status   `xml:"status"`
	ResponseDescription string   `xml:"responsedescription,omitempty"`
	Error               *Error   `xml:"error,omitempty"`
}

type Prop struct {
	XMLName xml.Name      `xml:"DAV: prop"`
	Raw     []RawXMLValue `xml:",any"`
}

type PrincipalCollectionSet struct {
	XMLName xml.Name `xml:"DAV: principal-collection-set"`
	Hrefs   []Href   `xml:"href"`
}

type ResourceType struct {
	XMLName    xml.Name  `xml:"DAV: resourcetype"`
	Collection *struct{} `xml:"DAV: collection,omitempty"`
	Principal  *struct{} `xml:"DAV: principal,omitempty"`
	Calendar   *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar,omitempty"`
}

type SupportedCalData struct {
	XMLName     xml.Name `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-data"`
	ContentType string   `xml:"content-type,attr"`
	Version     string   `xml:"version,attr,omitempty"`
}

type SupportedCollationSet struct {
	XMLName            xml.Name             `xml:"urn:ietf:params:xml:ns:caldav supported-collation-set"`
	SupportedCollation []SupportedCollation `xml:"urn:ietf:params:xml:ns:caldav supported-collation"`
}

type SupportedCollation struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav supported-collation"`
	Value   string   `xml:",chardata"`
}

type SupportedCompSet struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav supported-calendar-component-set"`
	Comp    []Comp   `xml:"urn:ietf:params:xml:ns:caldav comp"`
}

type Comp struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav comp"`
	Name    string   `xml:"name,attr"`
}

type AclProp struct {
	XMLName xml.Name `xml:"DAV: acl"`
	ACE     []Ace    `xml:"DAV: ace"`
}

type Ace struct {
	XMLName   xml.Name `xml:"DAV: ace"`
	Principal Href     `xml:"DAV: principal>href"`
	Grant     Grant    `xml:"DAV: grant"`
}

type Grant struct {
	XMLName xml.Name `xml:"DAV: grant"`
	Privs   []Priv   `xml:"DAV: privilege"`
}

type Priv struct {
	XMLName      xml.Name  `xml:"DAV: privilege"`
	Read         *struct{} `xml:"DAV: read,omitempty"`
	WriteProps   *struct{} `xml:"DAV: write-properties,omitempty"`
	WriteContent *struct{} `xml:"DAV: write-content,omitempty"`
	Bind         *struct{} `xml:"DAV: bind,omitempty"`
	Unbind       *struct{} `xml:"DAV: unbind,omitempty"`
}

type CalendarHomeSet struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
	Hrefs   []Href   `xml:"href,omitempty"`
}

type SupportedReportSet struct {
	XMLName         xml.Name          `xml:"DAV: supported-report-set"`
	SupportedReport []SupportedReport `xml:"DAV: supported-report"`
}

type SupportedReport struct {
	XMLName xml.Name   `xml:"DAV: supported-report"`
	Report  ReportType `xml:"DAV: report"`
}

type ReportType struct {
	XMLName          xml.Name  `xml:"DAV: report"`
	CalendarQuery    *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar-query,omitempty"`
	CalendarMultiget *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget,omitempty"`
	FreeBusyQuery    *struct{} `xml:"urn:ietf:params:xml:ns:caldav free-busy-query,omitempty"`
	SyncCollection   *struct{} `xml:"DAV: sync-collection,omitempty"`
}

type PropRequest struct {
	GetETag      bool
	CalendarData bool
}

type PropContainer struct {
	XMLName xml.Name      `xml:"DAV: prop"`
	Raw     []RawXMLValue `xml:",any"`
}

type CalendarQuery struct {
	XMLName xml.Name       `xml:"urn:ietf:params:xml:ns:caldav calendar-query"`
	XmlnsD  string         `xml:"xmlns:D,attr,omitempty"`
	XmlnsC  string         `xml:"xmlns:C,attr,omitempty"`
	Prop    PropContainer  `xml:"DAV: prop"`
	Filter  CalendarFilter `xml:"urn:ietf:params:xml:ns:caldav filter"`
}

type CalendarMultiget struct {
	XMLName xml.Name      `xml:"urn:ietf:params:xml:ns:caldav calendar-multiget"`
	XmlnsD  string        `xml:"xmlns:D,attr,omitempty"`
	XmlnsC  string        `xml:"xmlns:C,attr,omitempty"`
	Prop    PropContainer `xml:"DAV: prop"`
	Hrefs   []string      `xml:"DAV: href"`
}

type SyncCollection struct {
	XMLName   xml.Name      `xml:"DAV: sync-collection"`
	XmlnsD    string        `xml:"xmlns:D,attr,omitempty"`
	SyncToken string        `xml:"DAV: sync-token"`
	Limit     *SyncLimit    `xml:"DAV: limit,omitempty"`
	SyncLevel string        `xml:"DAV: sync-level,omitempty"`
	Prop      PropContainer `xml:"DAV: prop,omitempty"`
}

type SyncLimit struct {
	XMLName  xml.Name `xml:"DAV: limit"`
	NResults int      `xml:"DAV: nresults"`
}

type CalendarFilter struct {
	XMLName    xml.Name   `xml:"urn:ietf:params:xml:ns:caldav filter"`
	CompFilter CompFilter `xml:"urn:ietf:params:xml:ns:caldav comp-filter"`
}

type CompFilter struct {
	XMLName    xml.Name    `xml:"urn:ietf:params:xml:ns:caldav comp-filter"`
	Name       string      `xml:"name,attr"`
	CompFilter *CompFilter `xml:"urn:ietf:params:xml:ns:caldav comp-filter,omitempty"`
	TimeRange  *TimeRange  `xml:"urn:ietf:params:xml:ns:caldav time-range,omitempty"`
}

type TimeRange struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav time-range"`
	Start   string   `xml:"start,attr,omitempty"`
	End     string   `xml:"end,attr,omitempty"`
}

type FreeBusyQuery struct {
	XMLName xml.Name   `xml:"urn:ietf:params:xml:ns:caldav free-busy-query"`
	XmlnsC  string     `xml:"xmlns:C,attr,omitempty"`
	Time    *TimeRange `xml:"urn:ietf:params:xml:ns:caldav time-range"`
}

type GetContentLength struct {
	XMLName xml.Name `xml:"DAV: getcontentlength"`
	Length  int64    `xml:",chardata"`
}

type GetContentType struct {
	XMLName xml.Name `xml:"DAV: getcontenttype"`
	Type    string   `xml:",chardata"`
}

type TimeText time.Time

func (t *TimeText) UnmarshalText(b []byte) error {
	tt, err := http.ParseTime(string(b))
	if err != nil {
		return err
	}
	*t = TimeText(tt)
	return nil
}

func (t *TimeText) MarshalText() ([]byte, error) {
	s := time.Time(*t).UTC().Format(http.TimeFormat)
	return []byte(s), nil
}

type GetLastModified struct {
	XMLName      xml.Name `xml:"DAV: getlastmodified"`
	LastModified TimeText `xml:",chardata"`
}

type GetETag struct {
	XMLName xml.Name `xml:"DAV: getetag"`
	ETag    ETag     `xml:",chardata"`
}

type ETag string

func (etag *ETag) UnmarshalText(b []byte) error {
	s, err := strconv.Unquote(string(b))
	if err != nil {
		return fmt.Errorf("webdav: failed to unquote ETag: %v", err)
	}
	*etag = ETag(s)
	return nil
}

func (etag ETag) MarshalText() ([]byte, error) {
	return []byte(etag.String()), nil
}

func (etag ETag) String() string {
	return fmt.Sprintf("%q", string(etag))
}

type Error struct {
	XMLName xml.Name      `xml:"DAV: error"`
	Raw     []RawXMLValue `xml:",any"`
}

type DisplayName struct {
	XMLName xml.Name `xml:"DAV: displayname"`
	Name    string   `xml:",chardata"`
}

type CurrentUserPrincipal struct {
	XMLName         xml.Name  `xml:"DAV: current-user-principal"`
	Href            Href      `xml:"href,omitempty"`
	Unauthenticated *struct{} `xml:"unauthenticated,omitempty"`
}
