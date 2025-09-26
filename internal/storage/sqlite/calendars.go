package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	_ "modernc.org/sqlite"
)

func (s *Store) CreateCalendar(c storage.Calendar, ownerGroup string, description string) error {
	return s.withTx(context.Background(), func(tx *sql.Tx) error {
		id := c.ID
		if id == "" {
			id = uuid.New().String()
		}
		ownerUser := c.OwnerUserID
		if ownerUser == "" {
			return fmt.Errorf("OwnerUserID required")
		}
		uri := c.URI
		if uri == "" {
			return fmt.Errorf("URI required")
		}
		displayName := c.DisplayName
		color := c.Color
		if color == "" {
			color = "#3174ad"
		}
		ctag := c.CTag
		if ctag == "" {
			ctag = uuid.New().String()
		}
		now := time.Now().UTC()

		grp := ownerGroup
		if grp == "" {
			grp = c.OwnerGroup
		}
		desc := description
		if desc == "" {
			desc = c.Description
		}

		_, err := tx.Exec(`
			INSERT INTO calendars (
				id, owner_user_id, owner_group, uri, display_name, description, color,
				ctag, created_at, updated_at, sync_seq, sync_token
			) VALUES (
				?, ?, ?, ?, ?, ?, ?,
				?, ?, ?, 0, 'seq:0'
			)
		`, id, ownerUser, grp, uri, displayName, desc, color, ctag, now, now)
		return err
	})
}

func (s *Store) DeleteCalendar(ownerUserID, calURI string) error {
	ctx := context.Background()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM calendars
		WHERE owner_user_id = ? AND uri = ?
	`, ownerUserID, calURI)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetCalendarByURI(ctx context.Context, uri string) (*storage.Calendar, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        FROM calendars WHERE uri = ?`, uri)
	var c storage.Calendar
	if err := row.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.Color, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) UpdateCalendarDisplayName(ctx context.Context, ownerUID, calURI string, displayName *string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE calendars
		SET display_name = ?, updated_at = datetime('now')
		WHERE owner_user_id = ? AND uri = ?
	`, displayName, ownerUID, calURI)
	return err
}

func (s *Store) ListCalendarsByOwnerUser(ctx context.Context, uid string) ([]*storage.Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        FROM calendars WHERE owner_user_id = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Calendar
	for rows.Next() {
		var c storage.Calendar
		if err := rows.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.Color, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *Store) ListAllCalendars(ctx context.Context) ([]*storage.Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        FROM calendars`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Calendar
	for rows.Next() {
		var c storage.Calendar
		if err := rows.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.Color, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *Store) UpdateCalendarColor(ctx context.Context, ownerUID, calURI, color string) error {
	_, err := s.db.ExecContext(ctx, `
        UPDATE calendars
        SET color = ?, updated_at = datetime('now')
        WHERE owner_user_id = ? AND uri = ?
    `, color, ownerUID, calURI)
	return err
}

func (s *Store) GetObject(ctx context.Context, calendarID, uid string) (*storage.Object, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at
		FROM calendar_objects WHERE calendar_id = ? AND uid = ?`, calendarID, uid)
	var o storage.Object
	if err := row.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) PutObject(ctx context.Context, obj *storage.Object) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if obj.ID == "" {
			obj.ID = randID()
		}
		if obj.ETag == "" {
			obj.ETag = randID()
		}
		_, err := tx.Exec(`
			INSERT INTO calendar_objects (
				id, calendar_id, uid, etag, data, component, start_at, end_at
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, ?
			)
			ON CONFLICT(calendar_id, uid) DO UPDATE SET
				etag = excluded.etag,
				data = excluded.data,
				component = excluded.component,
				start_at = excluded.start_at,
				end_at = excluded.end_at,
				updated_at = datetime('now')
		`, obj.ID, obj.CalendarID, obj.UID, obj.ETag, obj.Data, obj.Component, obj.StartAt, obj.EndAt)
		return err
	})
}

func (s *Store) DeleteObject(ctx context.Context, calendarID, uid, etag string) error {
	if etag == "" {
		_, err := s.db.ExecContext(ctx, `
			DELETE FROM calendar_objects WHERE calendar_id = ? AND uid = ?
		`, calendarID, uid)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM calendar_objects WHERE calendar_id = ? AND uid = ? AND etag = ?
	`, calendarID, uid, etag)
	return err
}

func (s *Store) ListObjects(ctx context.Context, calendarID string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	q := `
		SELECT id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at
		FROM calendar_objects
		WHERE calendar_id = ?`
	args := []interface{}{calendarID}
	if start != nil {
		q += " AND (start_at IS NULL OR end_at >= ?)"
		args = append(args, *start)
		if end != nil {
			q += " AND (end_at IS NULL OR start_at <= ?)"
			args = append(args, *end)
		}
	} else if end != nil {
		q += " AND (start_at IS NULL OR start_at <= ?)"
		args = append(args, *end)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.Object
	for rows.Next() {
		var o storage.Object
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *Store) NewCTag(ctx context.Context, calendarID string) (string, error) {
	var ctag string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		ctag = randID()
		_, err := tx.Exec(`UPDATE calendars SET ctag = ?, updated_at = datetime('now') WHERE id = ?`, ctag, calendarID)
		return err
	})
	return ctag, err
}

func randID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) ListObjectsByComponent(ctx context.Context, calendarID string, components []string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	q := `
		SELECT id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at
		FROM calendar_objects
		WHERE calendar_id = ?`
	args := []interface{}{calendarID}

	if len(components) > 0 {
		placeholders := make([]string, len(components))
		for i, component := range components {
			placeholders[i] = "?"
			args = append(args, component)
		}
		q += " AND component IN (" + fmt.Sprintf("%s", placeholders[0])
		for i := 1; i < len(placeholders); i++ {
			q += ", " + placeholders[i]
		}
		q += ")"
	}

	if start != nil {
		q += " AND (start_at IS NULL OR end_at >= ?)"
		args = append(args, *start)
	}
	if end != nil {
		q += " AND (end_at IS NULL OR start_at <= ?)"
		args = append(args, *end)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.Object
	for rows.Next() {
		var o storage.Object
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *Store) GetSyncInfo(ctx context.Context, calendarID string) (string, int64, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT sync_token, sync_seq FROM calendars WHERE id = ?
	`, calendarID)
	var token string
	var seq int64
	if err := row.Scan(&token, &seq); err != nil {
		return "", 0, err
	}
	return token, seq, nil
}

func (s *Store) ListChangesSince(ctx context.Context, calendarID string, sinceSeq int64, limit int) ([]storage.Change, int64, error) {
	q := `
		SELECT seq, uid, deleted
		FROM calendar_changes
		WHERE calendar_id = ? AND seq > ?
		ORDER BY seq ASC`
	args := []interface{}{calendarID, sinceSeq}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []storage.Change
	var last int64 = sinceSeq
	for rows.Next() {
		var c storage.Change
		if err := rows.Scan(&c.Seq, &c.UID, &c.Deleted); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
		last = c.Seq
	}
	return out, last, nil
}

func (s *Store) RecordChange(ctx context.Context, calendarID, uid string, deleted bool) (string, int64, error) {
	var newToken string
	var newSeq int64

	err := s.withTx(ctx, func(tx *sql.Tx) error {
		// increment seq and get new values
		err := tx.QueryRow(`
			UPDATE calendars
			SET sync_seq = sync_seq + 1,
				sync_token = 'seq:' || (sync_seq + 1)
			WHERE id = ?
			RETURNING sync_seq, sync_token
		`, calendarID).Scan(&newSeq, &newToken)
		if err != nil {
			return err
		}

		// insert change row
		_, err = tx.Exec(`
			INSERT INTO calendar_changes(calendar_id, seq, uid, deleted)
			VALUES (?, ?, ?, ?)
		`, calendarID, newSeq, uid, deleted)
		return err
	})

	return newToken, newSeq, err
}
