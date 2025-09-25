package ical

import "time"

// Add to ical/event.go or update existing Event struct
type Event struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time
	Duration     time.Duration
	IsAllDay     bool
	IsRecurring  bool
	RRule        string
	RDates       []time.Time
	ExDates      []time.Time
	RecurrenceID *time.Time

	// Scheduling properties
	Organizer string            // Email address of organizer
	Attendees []string          // Email addresses of attendees
	Method    string            // iTIP method (REQUEST, REPLY, etc.)
	Sequence  int               // For change tracking
	PartStat  map[string]string // Participation status per attendee

	RawData []byte
}

// IsSchedulingEvent determines if this event requires scheduling
func (e *Event) IsSchedulingEvent() bool {
	return e.Organizer != "" && len(e.Attendees) > 0
}

// GetParticipationStatus gets the participation status for a specific attendee
func (e *Event) GetParticipationStatus(attendeeEmail string) string {
	if e.PartStat == nil {
		return PartStatNeedsAction
	}

	if status, exists := e.PartStat[attendeeEmail]; exists {
		return status
	}

	return PartStatNeedsAction
}

// SetParticipationStatus sets the participation status for a specific attendee
func (e *Event) SetParticipationStatus(attendeeEmail, status string) {
	if e.PartStat == nil {
		e.PartStat = make(map[string]string)
	}
	e.PartStat[attendeeEmail] = status
}
