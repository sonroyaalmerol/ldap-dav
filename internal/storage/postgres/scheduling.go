package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

// CreateSchedulingInbox creates a scheduling inbox for a user
func (s *Store) CreateSchedulingInbox(ctx context.Context, ownerUserID, ownerGroup string) error {
	inboxURI := fmt.Sprintf("calendar-inbox/%s", ownerUserID)

	_, err := s.pool.Exec(ctx, `
		insert into calendars (
			id, owner_user_id, owner_group, uri, display_name, description, 
			color, ctag, created_at, updated_at, sync_seq, sync_token, 
			calendar_type
		) values (
			$1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $9, 0, 'seq:0', 'inbox'
		)
		on conflict (owner_user_id, uri) do nothing
	`, uuid.New().String(), ownerUserID, ownerGroup, inboxURI,
		"Scheduling Inbox", "Calendar scheduling inbox", "#ff0000",
		uuid.New().String(), time.Now().UTC())
	return err
}

// CreateSchedulingOutbox creates a scheduling outbox for a user
func (s *Store) CreateSchedulingOutbox(ctx context.Context, ownerUserID, ownerGroup string) error {
	outboxURI := fmt.Sprintf("calendar-outbox/%s", ownerUserID)

	_, err := s.pool.Exec(ctx, `
		insert into calendars (
			id, owner_user_id, owner_group, uri, display_name, description, 
			color, ctag, created_at, updated_at, sync_seq, sync_token, 
			calendar_type
		) values (
			$1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $9, 0, 'seq:0', 'outbox'
		)
		on conflict (owner_user_id, uri) do nothing
	`, uuid.New().String(), ownerUserID, ownerGroup, outboxURI,
		"Scheduling Outbox", "Calendar scheduling outbox", "#00ff00",
		uuid.New().String(), time.Now().UTC())
	return err
}

// GetSchedulingInbox retrieves the scheduling inbox for a user
func (s *Store) GetSchedulingInbox(ctx context.Context, ownerUserID string) (*storage.Calendar, error) {
	inboxURI := fmt.Sprintf("calendar-inbox/%s", ownerUserID)
	return s.GetCalendarByURI(ctx, inboxURI)
}

// GetSchedulingOutbox retrieves the scheduling outbox for a user
func (s *Store) GetSchedulingOutbox(ctx context.Context, ownerUserID string) (*storage.Calendar, error) {
	outboxURI := fmt.Sprintf("calendar-outbox/%s", ownerUserID)
	return s.GetCalendarByURI(ctx, outboxURI)
}

// StoreSchedulingObject stores a scheduling object (invitation, response, etc.)
func (s *Store) StoreSchedulingObject(ctx context.Context, obj *storage.SchedulingObject) error {
	if obj.ID == "" {
		obj.ID = randID()
	}
	if obj.ETag == "" {
		obj.ETag = randID()
	}

	_, err := s.pool.Exec(ctx, `
		insert into scheduling_objects (
			id, calendar_id, uid, etag, data, method, recipient, originator,
			status, created_at, updated_at
		) values (
			$1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $10
		)
		on conflict (calendar_id, uid, recipient) do update set
			etag = excluded.etag,
			data = excluded.data,
			method = excluded.method,
			status = excluded.status,
			updated_at = now()
	`, obj.ID, obj.CalendarID, obj.UID, obj.ETag, obj.Data, obj.Method,
		obj.Recipient, obj.Originator, obj.Status, time.Now().UTC())
	return err
}

// GetSchedulingObject retrieves a scheduling object
func (s *Store) GetSchedulingObject(ctx context.Context, calendarID, uid, recipient string) (*storage.SchedulingObject, error) {
	row := s.pool.QueryRow(ctx, `
		select id::text, calendar_id::text, uid, etag, data, method, 
			   recipient, originator, status, created_at, updated_at
		from scheduling_objects 
		where calendar_id::text = $1 and uid = $2 and recipient = $3
	`, calendarID, uid, recipient)

	var obj storage.SchedulingObject
	err := row.Scan(&obj.ID, &obj.CalendarID, &obj.UID, &obj.ETag, &obj.Data,
		&obj.Method, &obj.Recipient, &obj.Originator, &obj.Status,
		&obj.CreatedAt, &obj.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &obj, nil
}

// ListSchedulingObjects lists scheduling objects in a calendar (inbox/outbox)
func (s *Store) ListSchedulingObjects(ctx context.Context, calendarID string) ([]*storage.SchedulingObject, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, calendar_id::text, uid, etag, data, method,
			   recipient, originator, status, created_at, updated_at
		from scheduling_objects
		where calendar_id::text = $1
		order by created_at desc
	`, calendarID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.SchedulingObject
	for rows.Next() {
		var obj storage.SchedulingObject
		if err := rows.Scan(&obj.ID, &obj.CalendarID, &obj.UID, &obj.ETag,
			&obj.Data, &obj.Method, &obj.Recipient, &obj.Originator,
			&obj.Status, &obj.CreatedAt, &obj.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &obj)
	}
	return out, nil
}

// DeleteSchedulingObject removes a scheduling object
func (s *Store) DeleteSchedulingObject(ctx context.Context, calendarID, uid, recipient string) error {
	cmdTag, err := s.pool.Exec(ctx, `
		delete from scheduling_objects
		where calendar_id::text = $1 and uid = $2 and recipient = $3
	`, calendarID, uid, recipient)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteOldSchedulingObjects(ctx context.Context, cutoff time.Time) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM scheduling_objects 
		WHERE created_at < $1 AND (status = 'delivered' OR status = 'processed')
	`, cutoff)
	return err
}

func (s *Store) DeleteOldAttendeeResponses(ctx context.Context, cutoff time.Time) error {
	// Delete responses for events that ended before the cutoff
	_, err := s.pool.Exec(ctx, `
		DELETE FROM attendee_responses 
		WHERE created_at < $1 
		AND event_uid IN (
			SELECT uid FROM calendar_objects 
			WHERE end_at IS NOT NULL AND end_at < $1
		)
	`, cutoff)
	return err
}

func (s *Store) DeleteOldFreeBusyInfo(ctx context.Context, cutoff time.Time) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM freebusy_info 
		WHERE end_time < $1
	`, cutoff)
	return err
}

// UpdateSchedulingObjectStatus updates the status of a scheduling object
func (s *Store) UpdateSchedulingObjectStatus(ctx context.Context, calendarID, uid, recipient, status string) error {
	_, err := s.pool.Exec(ctx, `
		update scheduling_objects
		set status = $1, updated_at = now()
		where calendar_id::text = $2 and uid = $3 and recipient = $4
	`, status, calendarID, uid, recipient)
	return err
}

// StoreAttendeeResponse stores an attendee's response to an invitation
func (s *Store) StoreAttendeeResponse(ctx context.Context, response *storage.AttendeeResponse) error {
	if response.ID == "" {
		response.ID = randID()
	}

	_, err := s.pool.Exec(ctx, `
		insert into attendee_responses (
			id, event_uid, calendar_id, attendee_email, response_status,
			response_data, created_at, updated_at
		) values (
			$1::uuid, $2, $3::uuid, $4, $5, $6, $7, $7
		)
		on conflict (event_uid, attendee_email) do update set
			response_status = excluded.response_status,
			response_data = excluded.response_data,
			updated_at = now()
	`, response.ID, response.EventUID, response.CalendarID, response.AttendeeEmail,
		response.ResponseStatus, response.ResponseData, time.Now().UTC())
	return err
}

// GetAttendeeResponse retrieves an attendee's response
func (s *Store) GetAttendeeResponse(ctx context.Context, eventUID, attendeeEmail string) (*storage.AttendeeResponse, error) {
	row := s.pool.QueryRow(ctx, `
		select id::text, event_uid, calendar_id::text, attendee_email,
			   response_status, response_data, created_at, updated_at
		from attendee_responses
		where event_uid = $1 and attendee_email = $2
	`, eventUID, attendeeEmail)

	var response storage.AttendeeResponse
	err := row.Scan(&response.ID, &response.EventUID, &response.CalendarID,
		&response.AttendeeEmail, &response.ResponseStatus, &response.ResponseData,
		&response.CreatedAt, &response.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &response, nil
}

// ListAttendeeResponses lists all responses for an event
func (s *Store) ListAttendeeResponses(ctx context.Context, eventUID string) ([]*storage.AttendeeResponse, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, event_uid, calendar_id::text, attendee_email,
			   response_status, response_data, created_at, updated_at
		from attendee_responses
		where event_uid = $1
		order by created_at desc
	`, eventUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.AttendeeResponse
	for rows.Next() {
		var response storage.AttendeeResponse
		if err := rows.Scan(&response.ID, &response.EventUID, &response.CalendarID,
			&response.AttendeeEmail, &response.ResponseStatus, &response.ResponseData,
			&response.CreatedAt, &response.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &response)
	}
	return out, nil
}

// StoreFreeBusyInfo stores free/busy information for scheduling
func (s *Store) StoreFreeBusyInfo(ctx context.Context, info *storage.FreeBusyInfo) error {
	if info.ID == "" {
		info.ID = randID()
	}

	_, err := s.pool.Exec(ctx, `
		insert into freebusy_info (
			id, user_id, start_time, end_time, busy_type, event_uid,
			created_at, updated_at
		) values (
			$1::uuid, $2, $3, $4, $5, $6, $7, $7
		)
		on conflict (user_id, start_time, end_time, event_uid) do update set
			busy_type = excluded.busy_type,
			updated_at = now()
	`, info.ID, info.UserID, info.StartTime, info.EndTime, info.BusyType,
		info.EventUID, time.Now().UTC())
	return err
}

// GetFreeBusyInfo retrieves free/busy information for a user within a time range
func (s *Store) GetFreeBusyInfo(ctx context.Context, userID string, start, end time.Time) ([]*storage.FreeBusyInfo, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, user_id, start_time, end_time, busy_type, event_uid,
			   created_at, updated_at
		from freebusy_info
		where user_id = $1 
		  and start_time < $3 
		  and end_time > $2
		order by start_time
	`, userID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.FreeBusyInfo
	for rows.Next() {
		var info storage.FreeBusyInfo
		if err := rows.Scan(&info.ID, &info.UserID, &info.StartTime, &info.EndTime,
			&info.BusyType, &info.EventUID, &info.CreatedAt, &info.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &info)
	}
	return out, nil
}

// DeleteFreeBusyInfo removes free/busy information for an event
func (s *Store) DeleteFreeBusyInfo(ctx context.Context, userID, eventUID string) error {
	_, err := s.pool.Exec(ctx, `
		delete from freebusy_info
		where user_id = $1 and event_uid = $2
	`, userID, eventUID)
	return err
}

// GetPendingSchedulingObjects gets objects that need processing
func (s *Store) GetPendingSchedulingObjects(ctx context.Context, limit int) ([]*storage.SchedulingObject, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, calendar_id::text, uid, etag, data, method,
			   recipient, originator, status, created_at, updated_at
		from scheduling_objects
		where status = 'pending'
		order by created_at
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.SchedulingObject
	for rows.Next() {
		var obj storage.SchedulingObject
		if err := rows.Scan(&obj.ID, &obj.CalendarID, &obj.UID, &obj.ETag,
			&obj.Data, &obj.Method, &obj.Recipient, &obj.Originator,
			&obj.Status, &obj.CreatedAt, &obj.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &obj)
	}
	return out, nil
}
