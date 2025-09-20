package common

import "encoding/xml"

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

