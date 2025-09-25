package ical

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

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

func SerializeEvent(event *Event) ([]byte, error) {
	if event.RawData != nil {
		if event.RecurrenceID != nil {
			return modifyEventInstance(event.RawData, event)
		}
		return event.RawData, nil
	}

	return createEventData(event)
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

	start, isAllDay, err := ParseDateTime(dtstart.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid DTSTART: %w", err)
	}
	event.Start = start
	event.IsAllDay = isAllDay

	if dtend := comp.Props.Get(ical.PropDateTimeEnd); dtend != nil {
		end, _, err := ParseDateTime(dtend.Value)
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
		event.RDates = append(event.RDates, dates...)
	}
	if len(event.RDates) > 0 {
		event.IsRecurring = true
	}

	exdateProps := comp.Props.Values(ical.PropExceptionDates)
	for _, exdateProp := range exdateProps {
		dates, err := parseMultipleDates(exdateProp.Value)
		if err != nil {
			continue
		}
		event.ExDates = append(event.ExDates, dates...)
	}

	if recID := comp.Props.Get(ical.PropRecurrenceID); recID != nil {
		recTime, _, err := ParseDateTime(recID.Value)
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
		rruleStr := "DTSTART:" + event.Start.Format("20060102T150405Z") + "\nRRULE:" + event.RRule
		rule, err := rrule.StrToRRule(rruleStr)
		if err != nil {
			return nil, fmt.Errorf("invalid RRULE: %w", err)
		}

		extendedEnd := rangeEnd.Add(event.Duration)
		occurrences := rule.Between(rangeStart.Add(-event.Duration), extendedEnd, true)
		instances = append(instances, occurrences...)
	}

	instances = append(instances, event.RDates...)

	instances = filterExcludedDates(instances, event.ExDates)

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
