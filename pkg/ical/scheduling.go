package ical

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// iTIP Methods as defined in RFC 5546
const (
	MethodPublish        = "PUBLISH"
	MethodRequest        = "REQUEST"
	MethodReply          = "REPLY"
	MethodAdd            = "ADD"
	MethodCancel         = "CANCEL"
	MethodRefresh        = "REFRESH"
	MethodCounter        = "COUNTER"
	MethodDeclineCounter = "DECLINECOUNTER"
)

// Participation Status values
const (
	PartStatNeedsAction = "NEEDS-ACTION"
	PartStatAccepted    = "ACCEPTED"
	PartStatDeclined    = "DECLINED"
	PartStatTentative   = "TENTATIVE"
	PartStatDelegated   = "DELEGATED"
)

type SchedulingMessage struct {
	Method     string
	Calendar   *Calendar
	Recipients []string
	Sender     string
}

// Calendar represents a full iCalendar with its components
type Calendar struct {
	Events  []*Event
	Method  string
	ProdID  string
	Version string
}

// SchedulingProcessor handles iTIP message processing
type SchedulingProcessor struct {
	prodID string
}

func NewSchedulingProcessor(prodID string) *SchedulingProcessor {
	return &SchedulingProcessor{
		prodID: prodID,
	}
}

// CreateRequestMessage creates an iTIP REQUEST message for inviting attendees
func (sp *SchedulingProcessor) CreateRequestMessage(event *Event, attendees []string) (*SchedulingMessage, error) {
	calendar := &Calendar{
		Events:  []*Event{event},
		Method:  MethodRequest,
		ProdID:  sp.prodID,
		Version: "2.0",
	}

	return &SchedulingMessage{
		Method:     MethodRequest,
		Calendar:   calendar,
		Recipients: attendees,
		Sender:     event.Organizer,
	}, nil
}

// CreateReplyMessage creates an iTIP REPLY message for attendee responses
func (sp *SchedulingProcessor) CreateReplyMessage(event *Event, attendeeEmail string, partStat string) (*SchedulingMessage, error) {
	replyEvent := &Event{
		UID:         event.UID,
		Summary:     event.Summary,
		Start:       event.Start,
		End:         event.End,
		Organizer:   event.Organizer,
		Attendees:   []string{attendeeEmail}, // Only the replying attendee
		IsRecurring: false,                   // Strip recurrence from replies
		RawData:     event.RawData,
	}

	calendar := &Calendar{
		Events:  []*Event{replyEvent},
		Method:  MethodReply,
		ProdID:  sp.prodID,
		Version: "2.0",
	}

	return &SchedulingMessage{
		Method:     MethodReply,
		Calendar:   calendar,
		Recipients: []string{event.Organizer},
		Sender:     attendeeEmail,
	}, nil
}

// CreateCancelMessage creates an iTIP CANCEL message
func (sp *SchedulingProcessor) CreateCancelMessage(event *Event, attendees []string) (*SchedulingMessage, error) {
	calendar := &Calendar{
		Events:  []*Event{event},
		Method:  MethodCancel,
		ProdID:  sp.prodID,
		Version: "2.0",
	}

	return &SchedulingMessage{
		Method:     MethodCancel,
		Calendar:   calendar,
		Recipients: attendees,
		Sender:     event.Organizer,
	}, nil
}

// SerializeSchedulingMessage converts a scheduling message to iCalendar format
func (sp *SchedulingProcessor) SerializeSchedulingMessage(msg *SchedulingMessage) ([]byte, error) {
	cal := &ical.Calendar{
		Component: &ical.Component{
			Name: ical.CompCalendar,
			Props: ical.Props{
				ical.PropVersion:   []ical.Prop{{Name: ical.PropVersion, Value: msg.Calendar.Version}},
				ical.PropProductID: []ical.Prop{{Name: ical.PropProductID, Value: msg.Calendar.ProdID}},
				ical.PropMethod:    []ical.Prop{{Name: ical.PropMethod, Value: msg.Method}},
			},
		},
	}

	// Add events to calendar
	for _, event := range msg.Calendar.Events {
		eventComp, err := sp.eventToComponent(event, msg.Method)
		if err != nil {
			return nil, err
		}
		cal.Children = append(cal.Children, eventComp)
	}

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// eventToComponent converts an Event to an iCalendar component
func (sp *SchedulingProcessor) eventToComponent(event *Event, method string) (*ical.Component, error) {
	eventComp := &ical.Component{
		Name:  ical.CompEvent,
		Props: make(ical.Props),
	}

	// Required properties
	eventComp.Props.Set(&ical.Prop{Name: ical.PropUID, Value: event.UID})
	eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeStamp, Value: time.Now().UTC().Format("20060102T150405Z")})

	// DTSTART
	if event.IsAllDay {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeStart, Value: event.Start.Format("20060102")})
	} else {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeStart, Value: event.Start.Format("20060102T150405Z")})
	}

	// DTEND
	if event.Duration > 0 {
		if event.IsAllDay {
			eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeEnd, Value: event.End.Format("20060102")})
		} else {
			eventComp.Props.Set(&ical.Prop{Name: ical.PropDateTimeEnd, Value: event.End.Format("20060102T150405Z")})
		}
	}

	// Optional properties
	if event.Summary != "" {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropSummary, Value: event.Summary})
	}

	if event.Description != "" {
		eventComp.Props.Set(&ical.Prop{Name: ical.PropDescription, Value: event.Description})
	}

	// Organizer (required for scheduling)
	if event.Organizer != "" {
		orgProp := &ical.Prop{Name: ical.PropOrganizer, Value: "mailto:" + event.Organizer}
		eventComp.Props.Set(orgProp)
	}

	// Attendees
	for _, attendee := range event.Attendees {
		attProp := &ical.Prop{Name: ical.PropAttendee, Value: "mailto:" + attendee}

		// Set participation status based on method
		switch method {
		case MethodRequest:
			attProp.Params = make(ical.Params)
			attProp.Params[ical.ParamParticipationStatus] = []string{PartStatNeedsAction}
		case MethodReply:
			// For replies, the participation status should be set by the caller
			attProp.Params = make(ical.Params)
			attProp.Params[ical.ParamParticipationStatus] = []string{PartStatAccepted} // Default, should be configurable
		}

		eventComp.Props.Add(attProp)
	}

	// Add SEQUENCE for change tracking
	eventComp.Props.Set(&ical.Prop{Name: ical.PropSequence, Value: "0"})

	return eventComp, nil
}

// ParseSchedulingMessage parses an iTIP message from iCalendar data
func (sp *SchedulingProcessor) ParseSchedulingMessage(data []byte) (*SchedulingMessage, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to parse calendar: %w", err)
	}

	// Get METHOD
	methodProp := cal.Props.Get(ical.PropMethod)
	if methodProp == nil {
		return nil, fmt.Errorf("missing METHOD property")
	}

	msg := &SchedulingMessage{
		Method: methodProp.Value,
		Calendar: &Calendar{
			Method:  methodProp.Value,
			ProdID:  cal.Props.Get(ical.PropProductID).Value,
			Version: cal.Props.Get(ical.PropVersion).Value,
		},
	}

	// Parse events
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			event, err := parseEvent(comp, data)
			if err != nil {
				continue // Skip malformed events
			}
			msg.Calendar.Events = append(msg.Calendar.Events, event)
		}
	}

	return msg, nil
}

// IsSchedulingObject determines if an iCalendar object is a scheduling object
func IsSchedulingObject(data []byte) (bool, string, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return false, "", err
	}

	// Check for METHOD property
	methodProp := cal.Props.Get(ical.PropMethod)
	if methodProp != nil {
		return true, methodProp.Value, nil
	}

	// Check if any events have both ORGANIZER and ATTENDEE
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			organizer := comp.Props.Get(ical.PropOrganizer)
			attendees := comp.Props.Values(ical.PropAttendee)

			if organizer != nil && len(attendees) > 0 {
				return true, "", nil // Scheduling object without explicit method
			}
		}
	}

	return false, "", nil
}

// ExtractOrganizerAndAttendees extracts organizer and attendee information from iCalendar data
func ExtractOrganizerAndAttendees(data []byte) (organizer string, attendees []string, err error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return "", nil, err
	}

	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			// Get organizer
			if orgProp := comp.Props.Get(ical.PropOrganizer); orgProp != nil {
				organizer = strings.TrimPrefix(orgProp.Value, "mailto:")
			}

			// Get attendees
			for _, attProp := range comp.Props.Values(ical.PropAttendee) {
				attendee := strings.TrimPrefix(attProp.Value, "mailto:")
				attendees = append(attendees, attendee)
			}

			break // Only process first event
		}
	}

	return organizer, attendees, nil
}

// SetParticipationStatus sets the participation status for a specific attendee
func SetParticipationStatus(data []byte, attendeeEmail, partStat string) ([]byte, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}

	modified := false
	targetEmail := "mailto:" + attendeeEmail

	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			for _, attProp := range comp.Props.Values(ical.PropAttendee) {
				if strings.EqualFold(attProp.Value, targetEmail) {
					if attProp.Params == nil {
						attProp.Params = make(ical.Params)
					}
					attProp.Params[ical.ParamParticipationStatus] = []string{partStat}
					modified = true
				}
			}
		}
	}

	if !modified {
		return data, nil // No changes made
	}

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
