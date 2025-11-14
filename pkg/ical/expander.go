package ical

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/emersion/go-ical"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/teambition/rrule-go"
)

type Event struct {
	// Core identification
	UID          string
	RecurrenceID *time.Time

	// Time properties
	Start    time.Time
	End      time.Time
	Duration time.Duration
	IsAllDay bool

	// Recurrence properties
	IsRecurring bool
	RRule       string
	RDate       []time.Time // Renamed from RDates for consistency with RFC
	ExDate      []time.Time // Renamed from ExDates for consistency with RFC

	// Summary and content
	Summary     string
	Description string
	Location    string

	// Status and classification
	Status string // TENTATIVE, CONFIRMED, CANCELLED
	Class  string // PUBLIC, PRIVATE, CONFIDENTIAL
	Transp string // TRANSPARENT, OPAQUE

	// Versioning
	Sequence     int
	Created      time.Time
	LastModified time.Time
	DtStamp      time.Time

	// Participants
	Organizer string
	Attendees []string

	// Categories and metadata
	Categories []string
	URL        string
	Priority   int

	// Geographic
	Geo string // Latitude;Longitude

	// Resources
	Resources []string

	// Alarms
	Alarms []Alarm

	// Attachments
	Attachments []string

	// Raw data for fallback
	RawData []byte
}

type Alarm struct {
	Action      string        // DISPLAY, AUDIO, EMAIL
	Trigger     time.Duration // Relative trigger (e.g., -15 minutes)
	TriggerTime *time.Time    // Absolute trigger
	Description string
	Summary     string
	Attendees   []string
	Repeat      int
	Duration    time.Duration
}

type RecurrenceExpander struct {
	timeZone *time.Location
}

func NewRecurrenceExpander(tz *time.Location) *RecurrenceExpander {
	if tz == nil {
		tz = time.UTC
	}
	return &RecurrenceExpander{timeZone: tz}
}

func ParseCalendar(data []byte) ([]*Event, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to parse calendar: %w", err)
	}

	var events []*Event

	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}

		event, err := parseEvent(comp, data)
		if err != nil {
			continue // Skip malformed events
		}
		events = append(events, event)
	}

	return events, nil
}

func (re *RecurrenceExpander) ExpandRecurrencesWithExceptions(masterEvents []*Event, exceptionObjs []*storage.Object, rangeStart, rangeEnd time.Time) ([]*Event, error) {
	if len(masterEvents) == 0 {
		return nil, nil
	}

	masterEvent := masterEvents[0]

	// Parse exception events
	exceptionMap := make(map[string]*Event) // key: UTC timestamp of RECURRENCE-ID
	for _, excObj := range exceptionObjs {
		excEvents, err := ParseCalendar([]byte(excObj.Data))
		if err != nil {
			continue
		}
		for _, excEvent := range excEvents {
			if excEvent.RecurrenceID != nil {
				key := excEvent.RecurrenceID.UTC().Format("20060102T150405Z")
				exceptionMap[key] = excEvent
			}
		}
	}

	// If not recurring, just return the master (unless it's an exception-only scenario)
	if !masterEvent.IsRecurring {
		if len(exceptionMap) == 0 {
			if re.eventOverlapsRange(masterEvent, rangeStart, rangeEnd) {
				return []*Event{masterEvent}, nil
			}
			return nil, nil
		}
		// Has exceptions but no RRULE - unusual but return exceptions in range
		var results []*Event
		for _, exc := range exceptionMap {
			if re.eventOverlapsRange(exc, rangeStart, rangeEnd) {
				results = append(results, exc)
			}
		}
		return results, nil
	}

	// Expand the recurrence rule
	var instances []time.Time

	if masterEvent.RRule != "" {
		ropt, err := parseRRule(masterEvent.RRule, masterEvent.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid RRULE: %w", err)
		}

		rule, err := rrule.NewRRule(*ropt)
		if err != nil {
			return nil, fmt.Errorf("failed to create rrule: %w", err)
		}

		// Extend range to catch events that might overlap
		extendedEnd := rangeEnd.Add(masterEvent.Duration)
		occurrences := rule.Between(rangeStart.Add(-masterEvent.Duration), extendedEnd, true)
		instances = append(instances, occurrences...)
	}

	// Add RDATE instances
	instances = append(instances, masterEvent.RDate...)

	// Remove EXDATE instances FIRST (before checking exceptions)
	instances = filterExcludedDates(instances, masterEvent.ExDate)

	// Build final list: use exception if exists, otherwise use generated instance
	var expandedEvents []*Event
	seenInstances := make(map[string]bool) // Deduplicate

	for _, instanceTime := range instances {
		key := instanceTime.UTC().Format("20060102T150405Z")

		// Skip duplicates
		if seenInstances[key] {
			continue
		}
		seenInstances[key] = true

		var finalEvent *Event

		// Check if there's an exception for this instance
		if exc, hasException := exceptionMap[key]; hasException {
			// Use the exception (which has custom data)
			finalEvent = exc
			delete(exceptionMap, key) // Mark as processed
		} else {
			// Generate instance from master
			eventEnd := instanceTime.Add(masterEvent.Duration)

			// Check if in range
			if !re.timeRangeOverlaps(instanceTime, eventEnd, rangeStart, rangeEnd) {
				continue
			}

			finalEvent = &Event{
				UID:          masterEvent.UID,
				Start:        instanceTime,
				End:          eventEnd,
				Duration:     masterEvent.Duration,
				RecurrenceID: &instanceTime,
				IsRecurring:  false,
				RRule:        "",
				RDate:        nil,
				ExDate:       nil,
				Summary:      masterEvent.Summary,
				Description:  masterEvent.Description,
				Location:     masterEvent.Location,
				Status:       masterEvent.Status,
				Class:        masterEvent.Class,
				Transp:       masterEvent.Transp,
				IsAllDay:     masterEvent.IsAllDay,
				Sequence:     masterEvent.Sequence,
				Created:      masterEvent.Created,
				LastModified: masterEvent.LastModified,
				DtStamp:      masterEvent.DtStamp,
				Organizer:    masterEvent.Organizer,
				Attendees:    masterEvent.Attendees,
				Categories:   masterEvent.Categories,
				URL:          masterEvent.URL,
				Geo:          masterEvent.Geo,
				Priority:     masterEvent.Priority,
				Resources:    masterEvent.Resources,
				Alarms:       masterEvent.Alarms,
				Attachments:  masterEvent.Attachments,
				RawData:      masterEvent.RawData,
			}
		}

		expandedEvents = append(expandedEvents, finalEvent)
	}

	// Add any exceptions that didn't match an RRULE instance (thisandfuture edits, standalone exceptions)
	for key, exc := range exceptionMap {
		if !seenInstances[key] && re.eventOverlapsRange(exc, rangeStart, rangeEnd) {
			expandedEvents = append(expandedEvents, exc)
			seenInstances[key] = true
		}
	}

	// Sort by start time
	sort.Slice(expandedEvents, func(i, j int) bool {
		return expandedEvents[i].Start.Before(expandedEvents[j].Start)
	})

	return expandedEvents, nil
}

func parseEvent(comp *ical.Component, originalData []byte) (*Event, error) {
	event := &Event{}

	if uid := comp.Props.Get(ical.PropUID); uid != nil {
		event.UID = uid.Value
	} else {
		return nil, fmt.Errorf("missing UID")
	}

	if summary := comp.Props.Get(ical.PropSummary); summary != nil {
		event.Summary = summary.Value
	}

	if desc := comp.Props.Get(ical.PropDescription); desc != nil {
		event.Description = desc.Value
	}

	dtstart := comp.Props.Get(ical.PropDateTimeStart)
	if dtstart == nil {
		return nil, fmt.Errorf("missing DTSTART")
	}

	start, isAllDay, err := parseDateTime(dtstart.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid DTSTART: %w", err)
	}
	event.Start = start
	event.IsAllDay = isAllDay

	if dtend := comp.Props.Get(ical.PropDateTimeEnd); dtend != nil {
		end, _, err := parseDateTime(dtend.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid DTEND: %w", err)
		}
		event.End = end
		event.Duration = end.Sub(start)
	} else if duration := comp.Props.Get(ical.PropDuration); duration != nil {
		dur, err := parseDuration(duration.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid DURATION: %w", err)
		}
		event.Duration = dur
		event.End = start.Add(dur)
	} else {
		// Default duration
		if isAllDay {
			event.Duration = 24 * time.Hour
		} else {
			event.Duration = 0
		}
		event.End = start.Add(event.Duration)
	}

	if rrule := comp.Props.Get(ical.PropRecurrenceRule); rrule != nil {
		event.RRule = rrule.Value
		event.IsRecurring = true
	}

	rdateProps := comp.Props.Values(ical.PropRecurrenceDates)
	for _, rdateProp := range rdateProps {
		dates, err := parseMultipleDates(rdateProp.Value)
		if err != nil {
			continue
		}
		event.RDate = append(event.RDate, dates...)
	}
	if len(event.RDate) > 0 {
		event.IsRecurring = true
	}

	exdateProps := comp.Props.Values(ical.PropExceptionDates)
	for _, exdateProp := range exdateProps {
		dates, err := parseMultipleDates(exdateProp.Value)
		if err != nil {
			continue
		}
		event.ExDate = append(event.ExDate, dates...)
	}

	if recID := comp.Props.Get(ical.PropRecurrenceID); recID != nil {
		recTime, _, err := parseDateTime(recID.Value)
		if err == nil {
			event.RecurrenceID = &recTime
		}
	}

	event.RawData = originalData

	return event, nil
}

func (re *RecurrenceExpander) eventOverlapsRange(event *Event, rangeStart, rangeEnd time.Time) bool {
	return re.timeRangeOverlaps(event.Start, event.End, rangeStart, rangeEnd)
}

func (re *RecurrenceExpander) timeRangeOverlaps(eventStart, eventEnd, rangeStart, rangeEnd time.Time) bool {
	return eventStart.Before(rangeEnd) && eventEnd.After(rangeStart)
}

func AddExceptionDate(event *Event, recurrenceID time.Time) {
	// Avoid duplicates
	for _, existing := range event.ExDate {
		if existing.Equal(recurrenceID) {
			return
		}
	}
	event.ExDate = append(event.ExDate, recurrenceID)
}

// RemoveExceptionDate removes a date from EXDATE list
func RemoveExceptionDate(event *Event, recurrenceID time.Time) {
	filtered := make([]time.Time, 0, len(event.ExDate))
	for _, exdate := range event.ExDate {
		if !exdate.Equal(recurrenceID) {
			filtered = append(filtered, exdate)
		}
	}
	event.ExDate = filtered
}

// HasExceptionDate checks if a date is in EXDATE
func HasExceptionDate(event *Event, recurrenceID time.Time) bool {
	for _, exdate := range event.ExDate {
		if exdate.Equal(recurrenceID) {
			return true
		}
	}
	return false
}
