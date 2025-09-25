package scheduling

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/rs/zerolog"
	"github.com/sonroyaalmerol/ldap-dav/internal/config"
	"github.com/sonroyaalmerol/ldap-dav/internal/directory"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

type Service struct {
	cfg    *config.Config
	store  storage.Store
	dir    directory.Directory
	logger zerolog.Logger
}

type SchedulingContext struct {
	UserID     string
	CalendarID string
	Organizer  string
	Attendees  []Attendee
	Method     string
}

type Attendee struct {
	URI        string
	Email      string
	Role       string
	Status     string
	CommonName string
}

func NewService(cfg *config.Config, store storage.Store, dir directory.Directory, logger zerolog.Logger) *Service {
	return &Service{
		cfg:    cfg,
		store:  store,
		dir:    dir,
		logger: logger,
	}
}

// ProcessSchedulingObject handles scheduling when calendar objects are modified
func (s *Service) ProcessSchedulingObject(ctx context.Context, userID string, oldObj, newObj *storage.Object) error {
	if newObj == nil && oldObj != nil {
		// DELETE operation
		return s.processDelete(ctx, userID, oldObj)
	}

	if oldObj == nil && newObj != nil {
		// CREATE operation
		return s.processCreate(ctx, userID, newObj)
	}

	if oldObj != nil && newObj != nil {
		// UPDATE operation
		return s.processUpdate(ctx, userID, oldObj, newObj)
	}

	return nil
}

func (s *Service) processCreate(ctx context.Context, userID string, obj *storage.Object) error {
	schedCtx, err := s.analyzeSchedulingObject(obj)
	if err != nil || schedCtx == nil {
		return err // Not a scheduling object or error
	}

	schedCtx.UserID = userID
	schedCtx.CalendarID = obj.CalendarID

	user, err := s.dir.LookupUserByAttr(ctx, "uid", userID)
	if err != nil {
		return fmt.Errorf("user lookup failed: %w", err)
	}

	isOrganizer := s.isUserOrganizer(user, schedCtx.Organizer)

	if isOrganizer {
		// Send REQUEST to all attendees
		schedCtx.Method = "REQUEST"
		return s.sendSchedulingMessages(ctx, schedCtx, obj)
	}

	// Attendee creating object - should not normally happen in CREATE
	s.logger.Debug().Str("user", userID).Str("organizer", schedCtx.Organizer).Msg("Attendee created scheduling object")
	return nil
}

func (s *Service) processUpdate(ctx context.Context, userID string, oldObj, newObj *storage.Object) error {
	newSchedCtx, err := s.analyzeSchedulingObject(newObj)
	if err != nil || newSchedCtx == nil {
		return err
	}

	oldSchedCtx, err := s.analyzeSchedulingObject(oldObj)
	if err != nil {
		return err
	}

	newSchedCtx.UserID = userID
	newSchedCtx.CalendarID = newObj.CalendarID

	user, err := s.dir.LookupUserByAttr(ctx, "uid", userID)
	if err != nil {
		return fmt.Errorf("user lookup failed: %w", err)
	}

	isOrganizer := s.isUserOrganizer(user, newSchedCtx.Organizer)

	if isOrganizer {
		// Check for significant changes that require notification
		if s.hasSignificantChange(oldObj, newObj) {
			newSchedCtx.Method = "REQUEST"
			return s.sendSchedulingMessages(ctx, newSchedCtx, newObj)
		}
	} else {
		// Check if attendee changed their participation status
		oldStatus := s.getAttendeeStatus(oldSchedCtx, user.Mail)
		newStatus := s.getAttendeeStatus(newSchedCtx, user.Mail)

		if oldStatus != newStatus && newStatus != "" {
			newSchedCtx.Method = "REPLY"
			return s.sendReplyMessage(ctx, newSchedCtx, newObj, user)
		}
	}

	return nil
}

func (s *Service) processDelete(ctx context.Context, userID string, obj *storage.Object) error {
	schedCtx, err := s.analyzeSchedulingObject(obj)
	if err != nil || schedCtx == nil {
		return err
	}

	schedCtx.UserID = userID
	schedCtx.CalendarID = obj.CalendarID

	user, err := s.dir.LookupUserByAttr(ctx, "uid", userID)
	if err != nil {
		return fmt.Errorf("user lookup failed: %w", err)
	}

	isOrganizer := s.isUserOrganizer(user, schedCtx.Organizer)

	if isOrganizer {
		// Send CANCEL to all attendees
		schedCtx.Method = "CANCEL"
		return s.sendSchedulingMessages(ctx, schedCtx, obj)
	}

	return nil
}

// analyzeSchedulingObject extracts scheduling information from calendar object
func (s *Service) analyzeSchedulingObject(obj *storage.Object) (*SchedulingContext, error) {
	if obj.Component != "VEVENT" {
		return nil, nil // Only handle events for now
	}

	cal, err := ical.NewDecoder(bytes.NewReader([]byte(obj.Data))).Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to parse calendar: %w", err)
	}

	var eventComp *ical.Component
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			eventComp = comp
			break
		}
	}

	if eventComp == nil {
		return nil, nil
	}

	// Check for ORGANIZER
	organizerProp := eventComp.Props.Get(ical.PropOrganizer)
	if organizerProp == nil {
		return nil, nil // Not a scheduling object
	}

	organizer := organizerProp.Value

	// Extract attendees
	var attendees []Attendee
	attendeeProps := eventComp.Props.Values(ical.PropAttendee)
	for _, attendeeProp := range attendeeProps {
		attendee := Attendee{
			URI:        attendeeProp.Value,
			Email:      strings.TrimPrefix(attendeeProp.Value, "mailto:"),
			Role:       attendeeProp.Params.Get("ROLE"),
			Status:     attendeeProp.Params.Get("PARTSTAT"),
			CommonName: attendeeProp.Params.Get("CN"),
		}

		if attendee.Status == "" {
			attendee.Status = "NEEDS-ACTION"
		}
		if attendee.Role == "" {
			attendee.Role = "REQ-PARTICIPANT"
		}

		attendees = append(attendees, attendee)
	}

	if len(attendees) == 0 {
		return nil, nil // Not a scheduling object
	}

	return &SchedulingContext{
		Organizer: organizer,
		Attendees: attendees,
	}, nil
}

func (s *Service) isUserOrganizer(user *directory.User, organizer string) bool {
	organizerEmail := strings.TrimPrefix(organizer, "mailto:")
	return strings.EqualFold(user.Mail, organizerEmail)
}

func (s *Service) hasSignificantChange(oldObj, newObj *storage.Object) bool {
	// Compare key properties that require attendee notification
	oldCal, err := ical.NewDecoder(bytes.NewReader([]byte(oldObj.Data))).Decode()
	if err != nil {
		return true
	}

	newCal, err := ical.NewDecoder(bytes.NewReader([]byte(newObj.Data))).Decode()
	if err != nil {
		return true
	}

	oldEvent := s.getEventComponent(oldCal)
	newEvent := s.getEventComponent(newCal)

	if oldEvent == nil || newEvent == nil {
		return true
	}

	// Check significant properties
	significantProps := []string{
		ical.PropDateTimeStart,
		ical.PropDateTimeEnd,
		ical.PropSummary,
		ical.PropLocation,
		ical.PropDescription,
	}

	for _, propName := range significantProps {
		oldProp := oldEvent.Props.Get(propName)
		newProp := newEvent.Props.Get(propName)

		oldValue := ""
		newValue := ""

		if oldProp != nil {
			oldValue = oldProp.Value
		}
		if newProp != nil {
			newValue = newProp.Value
		}

		if oldValue != newValue {
			return true
		}
	}

	return false
}

func (s *Service) getEventComponent(cal *ical.Calendar) *ical.Component {
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			return comp
		}
	}
	return nil
}

func (s *Service) getAttendeeStatus(schedCtx *SchedulingContext, userEmail string) string {
	for _, attendee := range schedCtx.Attendees {
		if strings.EqualFold(attendee.Email, userEmail) {
			return attendee.Status
		}
	}
	return ""
}

func (s *Service) sendSchedulingMessages(ctx context.Context, schedCtx *SchedulingContext, obj *storage.Object) error {
	// Create iTIP message
	itipData, err := s.createITIPMessage(obj, schedCtx.Method)
	if err != nil {
		return fmt.Errorf("failed to create iTIP message: %w", err)
	}

	// Extract UID for scheduling inbox
	uid := s.extractUID(obj.Data)

	for _, attendee := range schedCtx.Attendees {
		if err := s.deliverSchedulingMessage(ctx, attendee.Email, uid, schedCtx.Method, itipData); err != nil {
			s.logger.Error().Err(err).
				Str("attendee", attendee.Email).
				Str("method", schedCtx.Method).
				Msg("Failed to deliver scheduling message")
			// Continue with other attendees
		}
	}

	return nil
}

func (s *Service) sendReplyMessage(ctx context.Context, schedCtx *SchedulingContext, obj *storage.Object, user *directory.User) error {
	// Create iTIP REPLY message
	itipData, err := s.createITIPReply(obj, user)
	if err != nil {
		return fmt.Errorf("failed to create iTIP reply: %w", err)
	}

	// Extract organizer email
	organizerEmail := strings.TrimPrefix(schedCtx.Organizer, "mailto:")
	uid := s.extractUID(obj.Data)

	return s.deliverSchedulingMessage(ctx, organizerEmail, uid, "REPLY", itipData)
}

func (s *Service) createITIPMessage(obj *storage.Object, method string) ([]byte, error) {
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(obj.Data))).Decode()
	if err != nil {
		return nil, err
	}

	// Set METHOD property
	methodProp := &ical.Prop{
		Name:  ical.PropMethod,
		Value: method,
	}
	cal.Props.Set(methodProp)

	// Set PRODID
	prodIDProp := &ical.Prop{
		Name:  ical.PropProductID,
		Value: s.cfg.ICS.BuildProdID(),
	}
	cal.Props.Set(prodIDProp)

	// Ensure DTSTAMP is current for scheduling messages
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			dtstampProp := &ical.Prop{
				Name:  ical.PropDateTimeStamp,
				Value: time.Now().UTC().Format("20060102T150405Z"),
			}
			comp.Props.Set(dtstampProp)
		}
	}

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Service) createITIPReply(obj *storage.Object, user *directory.User) ([]byte, error) {
	cal, err := ical.NewDecoder(bytes.NewReader([]byte(obj.Data))).Decode()
	if err != nil {
		return nil, err
	}

	// Set METHOD to REPLY
	methodProp := &ical.Prop{
		Name:  ical.PropMethod,
		Value: "REPLY",
	}
	cal.Props.Set(methodProp)

	// Set PRODID
	prodIDProp := &ical.Prop{
		Name:  ical.PropProductID,
		Value: s.cfg.ICS.BuildProdID(),
	}
	cal.Props.Set(prodIDProp)

	// Filter event to only include replying attendee
	for _, comp := range cal.Children {
		if comp.Name == ical.CompEvent {
			// Update DTSTAMP
			dtstampProp := &ical.Prop{
				Name:  ical.PropDateTimeStamp,
				Value: time.Now().UTC().Format("20060102T150405Z"),
			}
			comp.Props.Set(dtstampProp)

			// Keep only the replying attendee
			attendeeProps := comp.Props.Values(ical.PropAttendee)
			comp.Props.Del(ical.PropAttendee)

			for _, attendeeProp := range attendeeProps {
				attendeeEmail := strings.TrimPrefix(attendeeProp.Value, "mailto:")
				if strings.EqualFold(attendeeEmail, user.Mail) {
					comp.Props.Set(&attendeeProp)
					break
				}
			}
		}
	}

	var buf bytes.Buffer
	enc := ical.NewEncoder(&buf)
	if err := enc.Encode(cal); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Service) deliverSchedulingMessage(ctx context.Context, email, uid, method string, message []byte) error {
	// First try to find internal user
	user, err := s.dir.LookupUserByAttr(ctx, "mail", email)
	if err == nil && user != nil {
		// Internal user - deliver to scheduling inbox
		return s.store.ProcessSchedulingMessage(ctx, user.UID, message, method)
	}

	// External user - would need email delivery (not implemented in this example)
	s.logger.Debug().
		Str("email", email).
		Str("method", method).
		Msg("External attendee - email delivery not implemented")

	return nil
}

func (s *Service) extractUID(icsData string) string {
	lines := strings.Split(icsData, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "UID:") {
			return strings.TrimPrefix(line, "UID:")
		}
	}
	return ""
}
