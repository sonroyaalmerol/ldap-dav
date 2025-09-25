package common

import (
	"encoding/xml"
)

// Scheduling Inbox/Outbox URLs
type ScheduleInboxURL struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav schedule-inbox-URL"`
	Href    Href     `xml:"href"`
}

type ScheduleOutboxURL struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav schedule-outbox-URL"`
	Href    Href     `xml:"href"`
}

// Calendar User Properties
type CalendarUserAddressSet struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-user-address-set"`
	Hrefs   []Href   `xml:"href"`
}

type CalendarUserType struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav calendar-user-type"`
	Value   string   `xml:",chardata"`
}

// Schedule Tag
type ScheduleTag struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav schedule-tag"`
	Tag     string   `xml:",chardata"`
}

// Schedule Default Calendar
type ScheduleDefaultCalendarURL struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:caldav schedule-default-calendar-URL"`
	Href    *Href    `xml:"href,omitempty"`
}

// Schedule Calendar Transparency
type ScheduleCalendarTransp struct {
	XMLName     xml.Name  `xml:"urn:ietf:params:xml:ns:caldav schedule-calendar-transp"`
	Opaque      *struct{} `xml:"urn:ietf:params:xml:ns:caldav opaque,omitempty"`
	Transparent *struct{} `xml:"urn:ietf:params:xml:ns:caldav transparent,omitempty"`
}

// Schedule Response
type ScheduleResponse struct {
	XMLName  xml.Name            `xml:"urn:ietf:params:xml:ns:caldav schedule-response"`
	Response []ScheduleRecipient `xml:"urn:ietf:params:xml:ns:caldav response"`
}

type ScheduleRecipient struct {
	XMLName       xml.Name `xml:"urn:ietf:params:xml:ns:caldav response"`
	Recipient     string   `xml:"urn:ietf:params:xml:ns:caldav recipient"`
	RequestStatus string   `xml:"urn:ietf:params:xml:ns:caldav request-status"`
	CalendarData  *string  `xml:"urn:ietf:params:xml:ns:caldav calendar-data,omitempty"`
}

func ScheduleInboxPath(basePath, userID string) string {
	return JoinURL(basePath, "calendars", userID, "inbox") + "/"
}

func ScheduleOutboxPath(basePath, userID string) string {
	return JoinURL(basePath, "calendars", userID, "outbox") + "/"
}
