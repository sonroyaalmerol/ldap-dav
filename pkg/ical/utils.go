package ical

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

func GenerateEventETag(event *Event) string {
	if event.RecurrenceID != nil {
		// For recurring instances, include recurrence ID in ETag
		return event.UID + "-" + event.RecurrenceID.Format("20060102T150405Z")
	}
	return event.UID + "-" + event.Start.Format("20060102T150405Z")
}

func parseDateTime(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(s)

	if len(s) == 8 {
		t, err := time.Parse("20060102", s)
		return t, true, err
	}

	if len(s) == 15 {
		t, err := time.ParseInLocation("20060102T150405", s, time.Local)
		return t, false, err
	}
	if len(s) == 16 && strings.HasSuffix(s, "Z") {
		t, err := time.Parse("20060102T150405Z", s)
		return t, false, err
	}

	t, err := time.Parse(time.RFC3339, s)
	return t, false, err
}

func parseMultipleDates(dateStr string) ([]time.Time, error) {
	var dates []time.Time
	parts := strings.Split(dateStr, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		date, _, err := parseDateTime(part)
		if err != nil {
			continue
		}
		dates = append(dates, date)
	}

	return dates, nil
}

func parseDuration(durStr string) (time.Duration, error) {
	durStr = strings.TrimSpace(durStr)
	if !strings.HasPrefix(durStr, "P") {
		return 0, fmt.Errorf("invalid duration format")
	}

	var days, hours, minutes, seconds int
	var inTime bool
	var current strings.Builder

	for _, r := range durStr[1:] {
		switch r {
		case 'D':
			if n, err := strconv.Atoi(current.String()); err == nil {
				days = n
			}
			current.Reset()
		case 'T':
			inTime = true
			current.Reset()
		case 'H':
			if inTime {
				if n, err := strconv.Atoi(current.String()); err == nil {
					hours = n
				}
			}
			current.Reset()
		case 'M':
			if inTime {
				if n, err := strconv.Atoi(current.String()); err == nil {
					minutes = n
				}
			}
			current.Reset()
		case 'S':
			if inTime {
				if n, err := strconv.Atoi(current.String()); err == nil {
					seconds = n
				}
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}

	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second, nil
}

func filterExcludedDates(instances, exdates []time.Time) []time.Time {
	if len(exdates) == 0 {
		return instances
	}

	excludeMap := make(map[string]bool)
	for _, exdate := range exdates {
		excludeMap[exdate.Format("20060102T150405Z")] = true
	}

	var filtered []time.Time
	for _, instance := range instances {
		key := instance.Format("20060102T150405Z")
		if !excludeMap[key] {
			filtered = append(filtered, instance)
		}
	}

	return filtered
}

func modifyEventInstance(rawData []byte, event *Event) ([]byte, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(rawData)).Decode()
	if err != nil {
		return nil, err
	}

	// Find the VEVENT component
	var eventComp *ical.Component
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			eventComp = comp
			break
		}
	}

	if eventComp == nil {
		return nil, fmt.Errorf("no VEVENT component found")
	}

	// Update DTSTART
	if dtstart := eventComp.Props.Get(ical.PropDateTimeStart); dtstart != nil {
		if event.IsAllDay {
			dtstart.Value = event.Start.Format("20060102")
		} else {
			dtstart.Value = event.Start.Format("20060102T150405Z")
		}
	}

	// Update DTEND
	if dtend := eventComp.Props.Get(ical.PropDateTimeEnd); dtend != nil {
		if event.IsAllDay {
			dtend.Value = event.End.Format("20060102")
		} else {
			dtend.Value = event.End.Format("20060102T150405Z")
		}
	}

	// Update UID
	if uid := eventComp.Props.Get(ical.PropUID); uid != nil {
		uid.Value = event.UID
	}

	// Add RECURRENCE-ID if this is a recurrence instance
	if event.RecurrenceID != nil {
		recurrenceID := &ical.Prop{
			Name: ical.PropRecurrenceID,
		}
		if event.IsAllDay {
			recurrenceID.Value = event.RecurrenceID.Format("20060102")
		} else {
			recurrenceID.Value = event.RecurrenceID.Format("20060102T150405Z")
		}
		eventComp.Props.Set(recurrenceID)
	}

	// Remove RRULE, RDATE, EXDATE from instances as they don't repeat
	if event.RecurrenceID != nil {
		eventComp.Props.Del(ical.PropRecurrenceRule)
		eventComp.Props.Del(ical.PropRecurrenceDates)
		eventComp.Props.Del(ical.PropExceptionDates)
	}

	// Serialize back to iCal
	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func createEventData(event *Event) ([]byte, error) {
	// Create a new calendar with basic properties
	cal := &ical.Calendar{
		Component: &ical.Component{
			Name: ical.CompCalendar,
			Props: ical.Props{
				ical.PropVersion:   []ical.Prop{{Name: ical.PropVersion, Value: "2.0"}},
				ical.PropProductID: []ical.Prop{{Name: ical.PropProductID, Value: "-//ldap-dav//EN"}},
			},
		},
	}

	// Create event component
	eventComp := &ical.Component{
		Name:  ical.CompEvent,
		Props: make(ical.Props),
	}

	// Basic properties
	eventComp.Props.Set(&ical.Prop{Name: ical.PropUID, Value: event.UID})
	eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeStamp, Value: time.Now().UTC().Format("20060102T150405Z")})

	// DTSTART
	if event.IsAllDay {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeStart, Value: event.Start.Format("20060102")})
	} else {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeStart, Value: event.Start.Format("20060102T150405Z")})
	}

	// DTEND if we have duration
	if event.Duration > 0 {
		if event.IsAllDay {
			eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeEnd, Value: event.End.Format("20060102")})
		} else {
			eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeEnd, Value: event.End.Format("20060102T150405Z")})
		}
	}

	if event.Summary != "" {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropSummary, Value: event.Summary})
	}

	if event.Description != "" {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropDescription, Value: event.Description})
	}

	if event.RecurrenceID != nil {
		if event.IsAllDay {
			eventComp.Props.Set(&ical.Prop{Name: ical.PropRecurrenceID, Value: event.RecurrenceID.Format("20060102")})
		} else {
			eventComp.Props.Set(&ical.Prop{Name: ical.PropRecurrenceID, Value: event.RecurrenceID.Format("20060102T150405Z")})
		}
	}

	cal.Children = []*ical.Component{eventComp}

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
