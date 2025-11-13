package ical

import (
	"bytes"
	"fmt"
	"time"

	"github.com/emersion/go-ical"
	"github.com/google/uuid"
)

// EventOperation represents the type of operation being performed
type EventOperation int

const (
	OpCreate EventOperation = iota
	OpUpdate
	OpDelete
)

// RecurrenceModification represents how a recurring event is being modified
type RecurrenceModification int

const (
	ModifyAll        RecurrenceModification = iota // Modify the master event
	ModifyThis                                     // Modify only this instance
	ModifyThisFuture                               // Modify this and future instances
)

// EventPutRequest represents a request to create or update an event
type EventPutRequest struct {
	Event                *Event
	ModificationType     RecurrenceModification
	OriginalRecurrenceID *time.Time // For modifying specific instances
	PreserveExceptions   bool       // Whether to preserve existing exceptions
}

// EventDeleteRequest represents a request to delete an event
type EventDeleteRequest struct {
	UID              string
	ModificationType RecurrenceModification
	RecurrenceID     *time.Time // For deleting specific instances
}

// RecurrenceManager handles complex recurring event operations
type RecurrenceManager struct {
	timeZone *time.Location
}

func NewRecurrenceManager(tz *time.Location) *RecurrenceManager {
	if tz == nil {
		tz = time.UTC
	}
	return &RecurrenceManager{timeZone: tz}
}

// PrepareEventPut prepares an event for storage, handling recurrence logic
func (rm *RecurrenceManager) PrepareEventPut(req *EventPutRequest) ([]*Event, error) {
	event := req.Event

	// Ensure UID exists
	if event.UID == "" {
		event.UID = uuid.New().String()
	}

	// Non-recurring event - simple case
	if !event.IsRecurring && req.ModificationType == ModifyAll {
		normalized, err := rm.normalizeEvent(event)
		if err != nil {
			return nil, fmt.Errorf("failed to normalize event: %w", err)
		}
		return []*Event{normalized}, nil
	}

	// Handle recurring event modifications
	switch req.ModificationType {
	case ModifyAll:
		return rm.prepareModifyAll(event, req.PreserveExceptions)

	case ModifyThis:
		return rm.prepareModifyThis(event, req.OriginalRecurrenceID)

	case ModifyThisFuture:
		return rm.prepareModifyThisFuture(event, req.OriginalRecurrenceID)

	default:
		return nil, fmt.Errorf("unsupported modification type")
	}
}

// PrepareEventDelete prepares deletion operations for events
func (rm *RecurrenceManager) PrepareEventDelete(req *EventDeleteRequest, masterEvent *Event) (*Event, error) {
	switch req.ModificationType {
	case ModifyAll:
		// Delete entire series - return nil to indicate full deletion
		return nil, nil

	case ModifyThis:
		// Add EXDATE to master event
		return rm.prepareDeleteThis(masterEvent, req.RecurrenceID)

	case ModifyThisFuture:
		// Truncate the series
		return rm.prepareDeleteThisFuture(masterEvent, req.RecurrenceID)

	default:
		return nil, fmt.Errorf("unsupported modification type")
	}
}

// normalizeEvent ensures the event has proper structure
func (rm *RecurrenceManager) normalizeEvent(event *Event) (*Event, error) {
	// If we have raw data, parse and reconstruct
	if event.RawData != nil {
		cal, err := ical.NewDecoder(bytes.NewReader(event.RawData)).Decode()
		if err != nil {
			return nil, err
		}

		// Find the event component
		var eventComp *ical.Component
		for _, comp := range cal.Children {
			if comp.Name == ical.CompEvent {
				eventComp = comp
				break
			}
		}

		if eventComp == nil {
			return nil, fmt.Errorf("no VEVENT found")
		}

		// Update DTSTAMP
		now := time.Now().UTC()
		eventComp.Props.Set(&ical.Prop{
			Name:  ical.PropDateTimeStamp,
			Value: now.Format("20060102T150405Z"),
		})

		// Re-serialize
		var buf bytes.Buffer
		enc := ical.NewEncoder(&buf)
		if err := enc.Encode(cal); err != nil {
			return nil, err
		}

		event.RawData = buf.Bytes()
	}

	return event, nil
}

// prepareModifyAll handles modifying all instances of a recurring event
func (rm *RecurrenceManager) prepareModifyAll(event *Event, preserveExceptions bool) ([]*Event, error) {
	if !preserveExceptions {
		// Simple case - just update the master event
		normalized, err := rm.normalizeEvent(event)
		if err != nil {
			return nil, err
		}
		return []*Event{normalized}, nil
	}

	// If preserving exceptions, we need to keep EXDATE entries
	// This requires parsing the original data if available
	normalized, err := rm.normalizeEvent(event)
	if err != nil {
		return nil, err
	}

	return []*Event{normalized}, nil
}

// prepareModifyThis handles modifying a single instance
func (rm *RecurrenceManager) prepareModifyThis(event *Event, recurrenceID *time.Time) ([]*Event, error) {
	if recurrenceID == nil {
		return nil, fmt.Errorf("recurrenceID required for ModifyThis")
	}

	// Create an exception instance with RECURRENCE-ID
	exception := &Event{
		UID:          event.UID,
		Summary:      event.Summary,
		Description:  event.Description,
		Start:        event.Start,
		End:          event.End,
		Duration:     event.Duration,
		IsAllDay:     event.IsAllDay,
		IsRecurring:  false,
		RecurrenceID: recurrenceID,
	}

	// Generate iCal data for the exception
	data, err := createEventData(exception)
	if err != nil {
		return nil, err
	}
	exception.RawData = data

	return []*Event{exception}, nil
}

// prepareModifyThisFuture handles modifying this and future instances
func (rm *RecurrenceManager) prepareModifyThisFuture(event *Event, splitPoint *time.Time) ([]*Event, error) {
	if splitPoint == nil {
		return nil, fmt.Errorf("splitPoint required for ModifyThisFuture")
	}

	var events []*Event

	// Create a truncated master event (ends before split point)
	if event.RawData != nil {
		truncated, err := rm.truncateRecurrence(event, *splitPoint)
		if err != nil {
			return nil, err
		}
		events = append(events, truncated)
	}

	// Create new master event starting from split point
	newMaster := &Event{
		UID:         uuid.New().String(), // New UID for new series
		Summary:     event.Summary,
		Description: event.Description,
		Start:       *splitPoint,
		End:         splitPoint.Add(event.Duration),
		Duration:    event.Duration,
		IsAllDay:    event.IsAllDay,
		IsRecurring: event.IsRecurring,
		RRule:       event.RRule,
	}

	data, err := createEventData(newMaster)
	if err != nil {
		return nil, err
	}
	newMaster.RawData = data

	events = append(events, newMaster)

	return events, nil
}

// truncateRecurrence truncates a recurring event to end before a specific date
func (rm *RecurrenceManager) truncateRecurrence(event *Event, until time.Time) (*Event, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(event.RawData)).Decode()
	if err != nil {
		return nil, err
	}

	var eventComp *ical.Component
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			eventComp = comp
			break
		}
	}

	if eventComp == nil {
		return nil, fmt.Errorf("no VEVENT found")
	}

	// Modify RRULE to add UNTIL
	if rruleProp := eventComp.Props.Get(ical.PropRecurrenceRule); rruleProp != nil {
		rruleValue := rruleProp.Value

		// Remove existing UNTIL or COUNT if present
		rruleValue = removeRRuleParameter(rruleValue, "UNTIL")
		rruleValue = removeRRuleParameter(rruleValue, "COUNT")

		// Add new UNTIL
		untilStr := until.Add(-1 * time.Second).Format("20060102T150405Z")
		if rruleValue != "" {
			rruleValue += ";UNTIL=" + untilStr
		}

		rruleProp.Value = rruleValue
	}

	// Update DTSTAMP
	eventComp.Props.Set(&ical.Prop{
		Name:  ical.PropDateTimeStamp,
		Value: time.Now().UTC().Format("20060102T150405Z"),
	})

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	truncatedEvent := *event
	truncatedEvent.RawData = buf.Bytes()

	return &truncatedEvent, nil
}

// prepareDeleteThis adds an EXDATE to the master event
func (rm *RecurrenceManager) prepareDeleteThis(masterEvent *Event, recurrenceID *time.Time) (*Event, error) {
	if recurrenceID == nil {
		return nil, fmt.Errorf("recurrenceID required")
	}

	cal, err := ical.NewDecoder(bytes.NewReader(masterEvent.RawData)).Decode()
	if err != nil {
		return nil, err
	}

	var eventComp *ical.Component
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			eventComp = comp
			break
		}
	}

	if eventComp == nil {
		return nil, fmt.Errorf("no VEVENT found")
	}

	// Add EXDATE
	exdateProp := &ical.Prop{
		Name: ical.PropExceptionDates,
	}

	if masterEvent.IsAllDay {
		exdateProp.Value = recurrenceID.Format("20060102")
	} else {
		exdateProp.Value = recurrenceID.Format("20060102T150405Z")
	}

	// Append to existing EXDATE or create new
	existingExdates := eventComp.Props.Values(ical.PropExceptionDates)
	if len(existingExdates) > 0 {
		lastExdate := eventComp.Props.Get(ical.PropExceptionDates)
		lastExdate.Value += "," + exdateProp.Value
	} else {
		eventComp.Props.Set(exdateProp)
	}

	// Update DTSTAMP
	eventComp.Props.Set(&ical.Prop{
		Name:  ical.PropDateTimeStamp,
		Value: time.Now().UTC().Format("20060102T150405Z"),
	})

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	modifiedEvent := *masterEvent
	modifiedEvent.RawData = buf.Bytes()

	return &modifiedEvent, nil
}

// prepareDeleteThisFuture truncates the series at the specified point
func (rm *RecurrenceManager) prepareDeleteThisFuture(masterEvent *Event, from *time.Time) (*Event, error) {
	if from == nil {
		return nil, fmt.Errorf("from date required")
	}

	return rm.truncateRecurrence(masterEvent, *from)
}

// Helper function to remove a parameter from RRULE string
func removeRRuleParameter(rrule, param string) string {
	// Simple implementation - you may want to make this more robust
	parts := []string{}
	for _, part := range splitRRule(rrule) {
		if !hasPrefix(part, param+"=") {
			parts = append(parts, part)
		}
	}
	return joinRRule(parts)
}

func splitRRule(s string) []string {
	var parts []string
	current := ""
	for _, c := range s {
		if c == ';' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func joinRRule(parts []string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += ";"
		}
		result += part
	}
	return result
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
