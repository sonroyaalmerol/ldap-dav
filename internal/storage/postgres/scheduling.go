package postgres

import (
	"context"
	"database/sql"
	"strings"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) ProcessSchedulingMessage(ctx context.Context, recipient string, icsData []byte, method string) error {
	uid := s.extractUIDFromICS(string(icsData))

	_, err := s.pool.Exec(ctx, `
        INSERT INTO scheduling_inbox (user_id, uid, method, data)
        VALUES ($1, $2, $3, $4)
    `, recipient, uid, method, string(icsData))

	return err
}

func (s *Store) GetSchedulingInboxObjects(ctx context.Context, userID string) ([]*storage.SchedulingMessage, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT id::text, user_id, uid, method, data, received_at, processed
        FROM scheduling_inbox
        WHERE user_id = $1 AND NOT processed
        ORDER BY received_at ASC
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*storage.SchedulingMessage
	for rows.Next() {
		var msg storage.SchedulingMessage
		err := rows.Scan(&msg.ID, &msg.UserID, &msg.UID, &msg.Method, &msg.Data, &msg.ReceivedAt, &msg.Processed)
		if err != nil {
			return nil, err
		}
		messages = append(messages, &msg)
	}

	return messages, nil
}

func (s *Store) DeleteSchedulingInboxObject(ctx context.Context, userID, uid string) error {
	_, err := s.pool.Exec(ctx, `
        DELETE FROM scheduling_inbox 
        WHERE user_id = $1 AND uid = $2
    `, userID, uid)
	return err
}

func (s *Store) GetScheduleTag(ctx context.Context, calendarID, uid string) (string, error) {
	var tag sql.NullString
	err := s.pool.QueryRow(ctx, `
        SELECT schedule_tag 
        FROM calendar_objects 
        WHERE calendar_id::text = $1 AND uid = $2
    `, calendarID, uid).Scan(&tag)

	if err != nil {
		return "", err
	}

	return tag.String, nil
}

func (s *Store) UpdateScheduleTag(ctx context.Context, calendarID, uid string) (string, error) {
	newTag := randID()

	_, err := s.pool.Exec(ctx, `
        UPDATE calendar_objects 
        SET schedule_tag = $1, updated_at = NOW()
        WHERE calendar_id::text = $2 AND uid = $3
    `, newTag, calendarID, uid)

	if err != nil {
		return "", err
	}

	return newTag, nil
}

func (s *Store) GetDefaultCalendar(ctx context.Context, userID string) (string, error) {
	var calendarID sql.NullString
	err := s.pool.QueryRow(ctx, `
        SELECT default_calendar_id::text
        FROM user_scheduling_settings
        WHERE user_id = $1
    `, userID).Scan(&calendarID)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	return calendarID.String, nil
}

func (s *Store) SetDefaultCalendar(ctx context.Context, userID, calendarID string) error {
	_, err := s.pool.Exec(ctx, `
        INSERT INTO user_scheduling_settings (user_id, default_calendar_id)
        VALUES ($1, $2::uuid)
        ON CONFLICT (user_id) DO UPDATE SET
            default_calendar_id = EXCLUDED.default_calendar_id,
            updated_at = NOW()
    `, userID, calendarID)

	return err
}

func (s *Store) GetCalendarTransparency(ctx context.Context, calendarID string) (string, error) {
	var transp string
	err := s.pool.QueryRow(ctx, `
        SELECT schedule_transp
        FROM calendars
        WHERE id::text = $1
    `, calendarID).Scan(&transp)

	return transp, err
}

func (s *Store) SetCalendarTransparency(ctx context.Context, calendarID string, transp string) error {
	_, err := s.pool.Exec(ctx, `
        UPDATE calendars
        SET schedule_transp = $1, updated_at = NOW()
        WHERE id::text = $2
    `, transp, calendarID)

	return err
}

// Helper function to extract UID from iCalendar data
func (s *Store) extractUIDFromICS(icsData string) string {
	lines := strings.Split(icsData, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "UID:") {
			return strings.TrimPrefix(line, "UID:")
		}
	}
	return randID() // fallback
}
