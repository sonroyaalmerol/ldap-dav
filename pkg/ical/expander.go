package ical

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/emersion/go-ical"
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

func (re *RecurrenceExpander) ExpandRecurrences(events []*Event, rangeStart, rangeEnd time.Time) ([]*Event, error) {
	var expandedEvents []*Event

	for _, event := range events {
		if !event.IsRecurring {
			if re.eventOverlapsRange(event, rangeStart, rangeEnd) {
				expandedEvents = append(expandedEvents, event)
			}
			continue
		}

		instances, err := re.expandEvent(event, rangeStart, rangeEnd)
		if err != nil {
			continue // Skip events that fail to expand
		}
		expandedEvents = append(expandedEvents, instances...)
	}

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

func (re *RecurrenceExpander) expandEvent(event *Event, rangeStart, rangeEnd time.Time) ([]*Event, error) {
	var instances []time.Time

	if event.RRule != "" {
		ropt, err := parseRRule(event.RRule, event.Start)
		if err != nil {
			return nil, fmt.Errorf("invalid RRULE: %w", err)
		}

		rule, err := rrule.NewRRule(*ropt)
		if err != nil {
			return nil, fmt.Errorf("failed to create rrule: %w", err)
		}

		extendedEnd := rangeEnd.Add(event.Duration)
		occurrences := rule.Between(rangeStart.Add(-event.Duration), extendedEnd, true)
		instances = append(instances, occurrences...)
	}

	instances = append(instances, event.RDate...)

	instances = filterExcludedDates(instances, event.ExDate)

	var filteredInstances []time.Time
	for _, instance := range instances {
		eventEnd := instance.Add(event.Duration)
		if re.timeRangeOverlaps(instance, eventEnd, rangeStart, rangeEnd) {
			filteredInstances = append(filteredInstances, instance)
		}
	}

	sort.Slice(filteredInstances, func(i, j int) bool {
		return filteredInstances[i].Before(filteredInstances[j])
	})

	var expandedEvents []*Event
	for i, instanceTime := range filteredInstances {
		instanceEvent := &Event{
			UID:          fmt.Sprintf("%s-%d", event.UID, i),
			Summary:      event.Summary,
			Description:  event.Description,
			Start:        instanceTime,
			End:          instanceTime.Add(event.Duration),
			Duration:     event.Duration,
			IsAllDay:     event.IsAllDay,
			IsRecurring:  false,
			RecurrenceID: &instanceTime,
			RawData:      event.RawData,
		}
		expandedEvents = append(expandedEvents, instanceEvent)
	}

	return expandedEvents, nil
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
