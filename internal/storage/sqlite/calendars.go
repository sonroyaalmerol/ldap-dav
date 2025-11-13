package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage/utils"
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
	// Get master event only (no RECURRENCE-ID in data)
	row := s.db.QueryRowContext(ctx, `
		SELECT id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at
		FROM calendar_objects 
		WHERE calendar_id = ? AND uid = ? 
		AND data NOT LIKE '%RECURRENCE-ID%'
		LIMIT 1
	`, calendarID, uid)

	var o storage.Object
	if err := row.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) PutObject(ctx context.Context, obj *storage.Object) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.putObjectInTx(tx, obj); err != nil {
			return err
		}

		if err := s.updateCalendarCTagInTx(tx, obj.CalendarID); err != nil {
			return err
		}

		if err := s.recordChangeInTx(tx, obj.CalendarID, obj.UID, false); err != nil {
			return err
		}

		return nil
	})
}

func (s *Store) putObjectInTx(tx *sql.Tx, obj *storage.Object) error {
	// Extract RECURRENCE-ID to determine if this is master or exception
	recurrenceID, err := utils.ExtractRecurrenceIDFromData(obj.Data)
	isException := (err == nil && recurrenceID != nil)

	var existingID string

	if isException {
		// Find existing exception by matching RECURRENCE-ID in data
		recIDValue := utils.ExtractRecurrenceIDValue(obj.Data)
		err := tx.QueryRow(`
			SELECT id FROM calendar_objects 
			WHERE calendar_id = ? AND uid = ? 
			AND data LIKE ?
			LIMIT 1
		`, obj.CalendarID, obj.UID, "%RECURRENCE-ID%"+recIDValue+"%").Scan(&existingID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	} else {
		// Master event: no RECURRENCE-ID
		err := tx.QueryRow(`
			SELECT id FROM calendar_objects 
			WHERE calendar_id = ? AND uid = ? 
			AND data NOT LIKE '%RECURRENCE-ID%'
			LIMIT 1
		`, obj.CalendarID, obj.UID).Scan(&existingID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}

	if existingID != "" {
		obj.ID = existingID
	} else if obj.ID == "" {
		obj.ID = uuid.Must(uuid.NewV7()).String()
	}

	obj.ETag = uuid.Must(uuid.NewV7()).String()

	// Try INSERT first
	_, err = tx.Exec(`
		INSERT INTO calendar_objects (
			id, calendar_id, uid, etag, data, component, start_at, end_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?
		)
	`, obj.ID, obj.CalendarID, obj.UID, obj.ETag, obj.Data, obj.Component, obj.StartAt, obj.EndAt)

	if err != nil {
		// INSERT failed, try UPDATE
		result, updateErr := tx.Exec(`
			UPDATE calendar_objects SET
				etag = ?,
				data = ?,
				component = ?,
				start_at = ?,
				end_at = ?,
				updated_at = datetime('now')
			WHERE id = ?
		`, obj.ETag, obj.Data, obj.Component, obj.StartAt, obj.EndAt, obj.ID)

		if updateErr != nil {
			return updateErr
		}

		rows, _ := result.RowsAffected()
		if rows == 0 {
			return fmt.Errorf("failed to insert or update object")
		}
	}

	return nil
}

func (s *Store) DeleteObject(ctx context.Context, calendarID, uid, etag string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var result sql.Result
		var err error

		// Delete ALL objects with this UID (master + exceptions)
		if etag == "" {
			result, err = tx.Exec(`
				DELETE FROM calendar_objects 
				WHERE calendar_id = ? AND uid = ?
			`, calendarID, uid)
		} else {
			// With ETag, only delete master first
			result, err = tx.Exec(`
				DELETE FROM calendar_objects 
				WHERE calendar_id = ? AND uid = ? 
				AND etag = ? 
				AND data NOT LIKE '%RECURRENCE-ID%'
			`, calendarID, uid, etag)

			if err == nil {
				rows, _ := result.RowsAffected()
				if rows > 0 {
					// Master deleted, now delete all exceptions
					_, err = tx.Exec(`
						DELETE FROM calendar_objects 
						WHERE calendar_id = ? AND uid = ? 
						AND data LIKE '%RECURRENCE-ID%'
					`, calendarID, uid)
				}
			}
		}

		if err != nil {
			return err
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return err
		}

		if rowsAffected == 0 {
			if etag != "" {
				var actualEtag string
				err := tx.QueryRow(`
					SELECT etag FROM calendar_objects 
					WHERE calendar_id = ? AND uid = ? 
					AND data NOT LIKE '%RECURRENCE-ID%'
					LIMIT 1
				`, calendarID, uid).Scan(&actualEtag)
				if err == sql.ErrNoRows {
					return sql.ErrNoRows
				}
				if err != nil {
					return err
				}
				return fmt.Errorf("etag mismatch: expected %s; got %s", etag, actualEtag)
			}
			return sql.ErrNoRows
		}

		if err := s.updateCalendarCTagInTx(tx, calendarID); err != nil {
			return err
		}

		if err := s.recordChangeInTx(tx, calendarID, uid, true); err != nil {
			return err
		}

		return nil
	})
}

func (s *Store) updateCalendarCTagInTx(tx *sql.Tx, calendarID string) error {
	ctag := uuid.Must(uuid.NewV7()).String()
	_, err := tx.Exec(`
		UPDATE calendars 
		SET ctag = ?, updated_at = datetime('now') 
		WHERE id = ?
	`, ctag, calendarID)
	return err
}

func (s *Store) recordChangeInTx(tx *sql.Tx, calendarID, uid string, deleted bool) error {
	var newSeq int64
	err := tx.QueryRow(`
		UPDATE calendars
		SET sync_seq = sync_seq + 1,
			sync_token = 'seq:' || (sync_seq + 1)
		WHERE id = ?
		RETURNING sync_seq
	`, calendarID).Scan(&newSeq)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO calendar_changes(calendar_id, seq, uid, deleted)
		VALUES (?, ?, ?, ?)
	`, calendarID, newSeq, uid, deleted)
	return err
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

func (s *Store) GetEventExceptions(ctx context.Context, calendarID, masterUID string) ([]*storage.Object, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at
		FROM calendar_objects
		WHERE calendar_id = ? AND uid = ?
		AND data LIKE '%RECURRENCE-ID%'
		ORDER BY start_at
	`, calendarID, masterUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var exceptions []*storage.Object
	for rows.Next() {
		var o storage.Object
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		exceptions = append(exceptions, &o)
	}
	return exceptions, nil
}
