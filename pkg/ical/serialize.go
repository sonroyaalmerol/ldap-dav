package ical

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	rrulelib "github.com/teambition/rrule-go"
)

// SerializeEventWithoutTimezone serializes an event without VTIMEZONE components
// and with all times in UTC format (per RFC 4791 Section 7.9.1 for expanded events)
func SerializeEventWithoutTimezone(event *Event) ([]byte, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//ldap-dav//EN")

	vevent := ical.NewEvent()

	// UID (required)
	vevent.Props.SetText(ical.PropUID, event.UID)

	// DTSTART (UTC format without timezone)
	vevent.Props.SetDateTime(ical.PropDateTimeStart, event.Start.UTC())

	// DTEND (UTC format without timezone)
	vevent.Props.SetDateTime(ical.PropDateTimeEnd, event.End.UTC())

	// RECURRENCE-ID (if present, UTC format)
	if event.RecurrenceID != nil {
		vevent.Props.SetDateTime(ical.PropRecurrenceID, event.RecurrenceID.UTC())
	}

	// DTSTAMP (required)
	if !event.DtStamp.IsZero() {
		vevent.Props.SetDateTime(ical.PropDateTimeStamp, event.DtStamp.UTC())
	} else {
		vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	}

	// SUMMARY
	if event.Summary != "" {
		vevent.Props.SetText(ical.PropSummary, event.Summary)
	}

	// DESCRIPTION
	if event.Description != "" {
		vevent.Props.SetText(ical.PropDescription, event.Description)
	}

	// LOCATION
	if event.Location != "" {
		vevent.Props.SetText(ical.PropLocation, event.Location)
	}

	// STATUS
	if event.Status != "" {
		vevent.Props.SetText(ical.PropStatus, event.Status)
	}

	// CLASS
	if event.Class != "" {
		vevent.Props.SetText(ical.PropClass, event.Class)
	}

	// TRANSP
	if event.Transp != "" {
		vevent.Props.SetText(ical.PropTransparency, event.Transp)
	}

	// SEQUENCE
	if event.Sequence > 0 {
		vevent.Props.SetText(ical.PropSequence, fmt.Sprintf("%d", event.Sequence))
	}

	// CREATED
	if !event.Created.IsZero() {
		vevent.Props.SetDateTime(ical.PropCreated, event.Created.UTC())
	}

	// LAST-MODIFIED
	if !event.LastModified.IsZero() {
		vevent.Props.SetDateTime(ical.PropLastModified, event.LastModified.UTC())
	}

	// ORGANIZER
	if event.Organizer != "" {
		vevent.Props.SetText(ical.PropOrganizer, event.Organizer)
	}

	// ATTENDEES
	for _, attendee := range event.Attendees {
		vevent.Props.SetText(ical.PropAttendee, attendee)
	}

	// CATEGORIES
	if len(event.Categories) > 0 {
		vevent.Props.SetText(ical.PropCategories, strings.Join(event.Categories, ","))
	}

	// URL
	if event.URL != "" {
		if u, err := url.Parse(event.URL); err == nil {
			vevent.Props.SetURI(ical.PropURL, u)
		} else {
			vevent.Props.SetText(ical.PropURL, event.URL)
		}
	}

	// PRIORITY
	if event.Priority > 0 {
		vevent.Props.SetText(ical.PropPriority, fmt.Sprintf("%d", event.Priority))
	}

	// GEO
	if event.Geo != "" {
		vevent.Props.SetText(ical.PropGeo, event.Geo)
	}

	// RESOURCES
	if len(event.Resources) > 0 {
		vevent.Props.SetText(ical.PropResources, strings.Join(event.Resources, ","))
	}

	// ALARMS
	for _, alarm := range event.Alarms {
		valarm := serializeAlarm(&alarm)
		vevent.Children = append(vevent.Children, valarm)
	}

	// NOTE: Explicitly NOT including:
	// - RRULE, RDATE, EXDATE (recurrence properties removed per RFC 4791 7.9.1)

	cal.Children = append(cal.Children, vevent.Component)

	// Encode to bytes
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// SerializeEvent serializes an event with all properties (including recurrence and timezone)
func SerializeEvent(event *Event) ([]byte, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//ldap-dav//EN")

	vevent := ical.NewEvent()

	// UID (required)
	vevent.Props.SetText(ical.PropUID, event.UID)

	// DTSTART
	if event.IsAllDay {
		vevent.Props.SetDate(ical.PropDateTimeStart, event.Start)
	} else {
		vevent.Props.SetDateTime(ical.PropDateTimeStart, event.Start)
	}

	// DTEND or DURATION
	if !event.End.IsZero() {
		if event.IsAllDay {
			vevent.Props.SetDate(ical.PropDateTimeEnd, event.End)
		} else {
			vevent.Props.SetDateTime(ical.PropDateTimeEnd, event.End)
		}
	} else if event.Duration > 0 {
		// Format duration manually (e.g., PT15M for 15 minutes)
		vevent.Props.SetText(ical.PropDuration, formatDuration(event.Duration))
	}

	// RECURRENCE-ID
	if event.RecurrenceID != nil {
		if event.IsAllDay {
			vevent.Props.SetDate(ical.PropRecurrenceID, *event.RecurrenceID)
		} else {
			vevent.Props.SetDateTime(ical.PropRecurrenceID, *event.RecurrenceID)
		}
	}

	// DTSTAMP (required)
	if !event.DtStamp.IsZero() {
		vevent.Props.SetDateTime(ical.PropDateTimeStamp, event.DtStamp)
	} else {
		vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	}

	// SUMMARY
	if event.Summary != "" {
		vevent.Props.SetText(ical.PropSummary, event.Summary)
	}

	// DESCRIPTION
	if event.Description != "" {
		vevent.Props.SetText(ical.PropDescription, event.Description)
	}

	// LOCATION
	if event.Location != "" {
		vevent.Props.SetText(ical.PropLocation, event.Location)
	}

	// STATUS
	if event.Status != "" {
		vevent.Props.SetText(ical.PropStatus, event.Status)
	}

	// CLASS
	if event.Class != "" {
		vevent.Props.SetText(ical.PropClass, event.Class)
	}

	// TRANSP
	if event.Transp != "" {
		vevent.Props.SetText(ical.PropTransparency, event.Transp)
	}

	// SEQUENCE
	if event.Sequence > 0 {
		vevent.Props.SetText(ical.PropSequence, fmt.Sprintf("%d", event.Sequence))
	}

	// CREATED
	if !event.Created.IsZero() {
		vevent.Props.SetDateTime(ical.PropCreated, event.Created)
	}

	// LAST-MODIFIED
	if !event.LastModified.IsZero() {
		vevent.Props.SetDateTime(ical.PropLastModified, event.LastModified)
	}

	// ORGANIZER
	if event.Organizer != "" {
		vevent.Props.SetText(ical.PropOrganizer, event.Organizer)
	}

	// ATTENDEES
	for _, attendee := range event.Attendees {
		vevent.Props.SetText(ical.PropAttendee, attendee)
	}

	// CATEGORIES
	if len(event.Categories) > 0 {
		vevent.Props.SetText(ical.PropCategories, strings.Join(event.Categories, ","))
	}

	// URL
	if event.URL != "" {
		if u, err := url.Parse(event.URL); err == nil {
			vevent.Props.SetURI(ical.PropURL, u)
		} else {
			vevent.Props.SetText(ical.PropURL, event.URL)
		}
	}

	// PRIORITY
	if event.Priority > 0 {
		vevent.Props.SetText(ical.PropPriority, fmt.Sprintf("%d", event.Priority))
	}

	// GEO
	if event.Geo != "" {
		vevent.Props.SetText(ical.PropGeo, event.Geo)
	}

	// RESOURCES
	if len(event.Resources) > 0 {
		vevent.Props.SetText(ical.PropResources, strings.Join(event.Resources, ","))
	}

	// Recurrence properties (only if recurring)
	if event.IsRecurring {
		if event.RRule != "" {
			// Parse RRule string into ROption
			ropt, err := parseRRule(event.RRule, event.Start)
			if err == nil {
				vevent.Props.SetRecurrenceRule(ropt)
			} else {
				vevent.Props.SetText(ical.PropRecurrenceRule, event.RRule)
			}
		}

		// RDATE
		if len(event.RDate) > 0 {
			rdates := make([]string, len(event.RDate))
			for i, rdate := range event.RDate {
				rdates[i] = rdate.UTC().Format("20060102T150405Z")
			}
			vevent.Props.Add(&ical.Prop{
				Name:  "RDATE",
				Value: strings.Join(rdates, ","),
			})
		}

		// EXDATE
		if len(event.ExDate) > 0 {
			exdates := make([]string, len(event.ExDate))
			for i, exdate := range event.ExDate {
				exdates[i] = exdate.UTC().Format("20060102T150405Z")
			}
			vevent.Props.Add(&ical.Prop{
				Name:  "EXDATE",
				Value: strings.Join(exdates, ","),
			})
		}
	}

	// ALARMS
	for _, alarm := range event.Alarms {
		valarm := serializeAlarm(&alarm)
		vevent.Children = append(vevent.Children, valarm)
	}

	cal.Children = append(cal.Children, vevent.Component)

	// Encode to bytes
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func serializeAlarm(alarm *Alarm) *ical.Component {
	valarm := &ical.Component{Name: ical.CompAlarm}

	// ACTION
	if alarm.Action != "" {
		valarm.Props.SetText(ical.PropAction, alarm.Action)
	}

	// TRIGGER
	if alarm.TriggerTime != nil {
		valarm.Props.SetDateTime(ical.PropTrigger, *alarm.TriggerTime)
	} else if alarm.Trigger != 0 {
		// Format as duration (e.g., -PT15M for 15 minutes before)
		valarm.Props.SetText(ical.PropTrigger, formatDuration(alarm.Trigger))
	}

	// DESCRIPTION
	if alarm.Description != "" {
		valarm.Props.SetText(ical.PropDescription, alarm.Description)
	}

	// SUMMARY (for EMAIL actions)
	if alarm.Summary != "" {
		valarm.Props.SetText(ical.PropSummary, alarm.Summary)
	}

	// ATTENDEES (for EMAIL actions)
	for _, attendee := range alarm.Attendees {
		valarm.Props.SetText(ical.PropAttendee, attendee)
	}

	// REPEAT
	if alarm.Repeat > 0 {
		valarm.Props.SetText(ical.PropRepeat, fmt.Sprintf("%d", alarm.Repeat))
	}

	// DURATION (used with REPEAT)
	if alarm.Duration > 0 {
		valarm.Props.SetText(ical.PropDuration, formatDuration(alarm.Duration))
	}

	return valarm
}

func parseRRule(rrule string, dtstart time.Time) (*rrulelib.ROption, error) {
	ropt, err := rrulelib.StrToROption(rrule)
	if err != nil {
		return nil, err
	}

	if ropt.Dtstart.IsZero() {
		ropt.Dtstart = dtstart
	}

	return ropt, nil
}

// formatDuration converts time.Duration to iCalendar duration format
// Examples: PT15M (15 minutes), PT1H (1 hour), -PT15M (15 minutes before)
func formatDuration(d time.Duration) string {
	negative := d < 0
	if negative {
		d = -d
	}

	var parts []string

	// Days
	if d >= 24*time.Hour {
		days := d / (24 * time.Hour)
		parts = append(parts, fmt.Sprintf("%dD", days))
		d -= days * 24 * time.Hour
	}

	// Time component
	var timeParts []string

	// Hours
	if d >= time.Hour {
		hours := d / time.Hour
		timeParts = append(timeParts, fmt.Sprintf("%dH", hours))
		d -= hours * time.Hour
	}

	// Minutes
	if d >= time.Minute {
		minutes := d / time.Minute
		timeParts = append(timeParts, fmt.Sprintf("%dM", minutes))
		d -= minutes * time.Minute
	}

	// Seconds
	if d > 0 {
		seconds := d / time.Second
		timeParts = append(timeParts, fmt.Sprintf("%dS", seconds))
	}

	result := "P"
	result += strings.Join(parts, "")
	if len(timeParts) > 0 {
		result += "T" + strings.Join(timeParts, "")
	}

	if negative {
		result = "-" + result
	}

	// Handle edge case where duration is 0
	if result == "P" {
		result = "PT0S"
	}

	return result
}

// SerializeMultipleEvents serializes multiple events (master + exceptions) into one VCALENDAR
func SerializeMultipleEvents(events []*Event) (string, error) {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//ldap-dav//EN")

	for _, event := range events {
		// Serialize each event and extract its VEVENT component
		eventData, err := SerializeEvent(event)
		if err != nil {
			return "", err
		}

		// Parse it back to extract the VEVENT
		dec := ical.NewDecoder(bytes.NewReader(eventData))
		tmpCal, err := dec.Decode()
		if err != nil {
			return "", err
		}

		// Add all children (VEVENTs) to our calendar
		for _, child := range tmpCal.Children {
			if child.Name == ical.CompEvent {
				cal.Children = append(cal.Children, child)
			}
		}
	}

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}

	return buf.String(), nil
}
