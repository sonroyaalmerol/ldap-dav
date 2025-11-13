package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage/utils"
)

func (s *Store) CreateCalendar(c storage.Calendar, ownerGroup string, description string) error {
	ctx := context.Background()

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

	_, err := s.pool.Exec(ctx, `
        insert into calendars (
          id, owner_user_id, owner_group, uri, display_name, description, color,
          ctag, created_at, updated_at, sync_seq, sync_token
        ) values (
          $1::uuid, $2, $3, $4, $5, $6, $7,
          $8, $9, $9, 0, 'seq:0'
        )
    `, id, ownerUser, grp, uri, displayName, desc, color, ctag, now)
	return err
}

func (s *Store) DeleteCalendar(ownerUserID, calURI string) error {
	ctx := context.Background()
	cmdTag, err := s.pool.Exec(ctx, `
		delete from calendars
		where owner_user_id = $1 and uri = $2
	`, ownerUserID, calURI)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) GetCalendarByURI(ctx context.Context, uri string) (*storage.Calendar, error) {
	row := s.pool.QueryRow(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        from calendars where uri = $1`, uri)
	var c storage.Calendar
	if err := row.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.Color, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) UpdateCalendarDisplayName(ctx context.Context, ownerUID, calURI string, displayName *string) error {
	_, err := s.pool.Exec(ctx, `
		update calendars
		set display_name = $1, updated_at = now()
		where owner_user_id = $2 and uri = $3
	`, displayName, ownerUID, calURI)
	return err
}

func (s *Store) ListCalendarsByOwnerUser(ctx context.Context, uid string) ([]*storage.Calendar, error) {
	rows, err := s.pool.Query(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        from calendars where owner_user_id = $1`, uid)
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
	rows, err := s.pool.Query(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        from calendars`)
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
	_, err := s.pool.Exec(ctx, `
        update calendars
        set color = $1, updated_at = now()
        where owner_user_id = $2 and uri = $3
    `, color, ownerUID, calURI)
	return err
}

func (s *Store) GetObject(ctx context.Context, calendarID, uid string) (*storage.Object, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, calendar_id::text, uid, etag, data, component, start_at, end_at, updated_at, recurrence_id
		FROM calendar_objects 
		WHERE calendar_id::text = $1 AND uid = $2 
		AND recurrence_id IS NULL
		LIMIT 1
	`, calendarID, uid)

	var o storage.Object
	var recurrenceID sql.NullString
	if err := row.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt, &recurrenceID); err != nil {
		return nil, err
	}
	if recurrenceID.Valid {
		o.RecurrenceID = recurrenceID.String
	}
	return &o, nil
}

func (s *Store) PutObject(ctx context.Context, obj *storage.Object) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Extract RECURRENCE-ID to determine if this is master or exception
	recurrenceID, err := utils.ExtractRecurrenceIDFromData(obj.Data)
	var recIDStr sql.NullString
	if err == nil && recurrenceID != nil {
		recIDStr = sql.NullString{String: recurrenceID.String(), Valid: true}
		obj.RecurrenceID = recurrenceID.String()
	}

	if err := s.putObjectInTx(ctx, tx, obj, recIDStr); err != nil {
		return err
	}

	if err := updateCalendarCTagInTx(ctx, tx, obj.CalendarID); err != nil {
		return err
	}

	if err := recordChangeInTx(ctx, tx, obj.CalendarID, obj.UID, false); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) DeleteObject(ctx context.Context, calendarID, uid, etag string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var cmdTag pgconn.CommandTag

	// Delete ALL objects with this UID (master + exceptions)
	if etag == "" {
		cmdTag, err = tx.Exec(ctx, `
			DELETE FROM calendar_objects WHERE calendar_id::text = $1 AND uid = $2
		`, calendarID, uid)
	} else {
		// With ETag, only delete master and then cascade delete exceptions
		cmdTag, err = tx.Exec(ctx, `
			DELETE FROM calendar_objects 
			WHERE calendar_id::text = $1 AND uid = $2 
			AND etag = $3 
			AND recurrence_id IS NULL
		`, calendarID, uid, etag)

		if err == nil && cmdTag.RowsAffected() > 0 {
			// Master deleted, now delete all exceptions
			_, err = tx.Exec(ctx, `
				DELETE FROM calendar_objects 
				WHERE calendar_id::text = $1 AND uid = $2 
				AND recurrence_id IS NOT NULL
			`, calendarID, uid)
		}
	}

	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() == 0 {
		if etag != "" {
			var actualEtag string
			err := tx.QueryRow(ctx, `
				SELECT etag FROM calendar_objects 
				WHERE calendar_id::text = $1 AND uid = $2 
				AND recurrence_id IS NULL
				LIMIT 1
			`, calendarID, uid).Scan(&actualEtag)
			if err == nil {
				return fmt.Errorf("etag mismatch: expected %s; got %s", etag, actualEtag)
			}
			if err != pgx.ErrNoRows {
				return err
			}
		}
		return sql.ErrNoRows
	}

	if err := updateCalendarCTagInTx(ctx, tx, calendarID); err != nil {
		return err
	}

	if err := recordChangeInTx(ctx, tx, calendarID, uid, true); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) ListObjectsByComponent(ctx context.Context, calendarID string, components []string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	q := `
		select id::text, calendar_id::text, uid, etag, data, component, start_at, end_at, updated_at, recurrence_id
		from calendar_objects
		where calendar_id::text = $1`
	args := []any{calendarID}

	if len(components) > 0 {
		q += " and component = any($2)"
		args = append(args, components)
	}
	argPos := len(args) + 1
	if start != nil {
		q += " and (start_at is null or end_at >= $" + strconv.FormatInt(int64(argPos), 10) + ")"
		args = append(args, *start)
		argPos++
	}
	if end != nil {
		q += " and (end_at is null or start_at <= $" + strconv.FormatInt(int64(argPos), 10) + ")"
		args = append(args, *end)
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.Object
	for rows.Next() {
		var o storage.Object
		var recurrenceID sql.NullString
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt, &recurrenceID); err != nil {
			return nil, err
		}
		if recurrenceID.Valid {
			o.RecurrenceID = recurrenceID.String
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *Store) GetSyncInfo(ctx context.Context, calendarID string) (string, int64, error) {
	row := s.pool.QueryRow(ctx, `
		select sync_token, sync_seq from calendars where id::text = $1
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
		select seq, uid, deleted
		from calendar_changes
		where calendar_id::text = $1 and seq > $2
		order by seq asc`
	args := []any{calendarID, sinceSeq}
	if limit > 0 {
		q += " limit $3"
		args = append(args, limit)
	}

	rows, err := s.pool.Query(ctx, q, args...)
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
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, calendar_id::text, uid, etag, data, component, start_at, end_at, updated_at, recurrence_id
		FROM calendar_objects
		WHERE calendar_id::text = $1 AND uid = $2
		AND recurrence_id IS NOT NULL
		ORDER BY start_at
	`, calendarID, masterUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var exceptions []*storage.Object
	for rows.Next() {
		var o storage.Object
		var recurrenceID sql.NullString
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt, &recurrenceID); err != nil {
			return nil, err
		}
		if recurrenceID.Valid {
			o.RecurrenceID = recurrenceID.String
		}
		exceptions = append(exceptions, &o)
	}
	return exceptions, nil
}

func (s *Store) putObjectInTx(ctx context.Context, tx pgx.Tx, obj *storage.Object, recIDStr sql.NullString) error {
	var existingID string
	var query string

	if recIDStr.Valid {
		query = `
			SELECT id::text FROM calendar_objects 
			WHERE calendar_id::text = $1 AND uid = $2 
			AND recurrence_id = $3
			LIMIT 1
		`
		err := tx.QueryRow(ctx, query, obj.CalendarID, obj.UID, recIDStr.String).Scan(&existingID)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
	} else {
		query = `
			SELECT id::text FROM calendar_objects 
			WHERE calendar_id::text = $1 AND uid = $2 
			AND recurrence_id IS NULL
			LIMIT 1
		`
		err := tx.QueryRow(ctx, query, obj.CalendarID, obj.UID).Scan(&existingID)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
	}

	if existingID != "" {
		obj.ID = existingID
	} else if obj.ID == "" {
		obj.ID = uuid.Must(uuid.NewV7()).String()
	}

	obj.ETag = uuid.Must(uuid.NewV7()).String()

	_, err := tx.Exec(ctx, `
		INSERT INTO calendar_objects (
			id, calendar_id, uid, etag, data, component, start_at, end_at, recurrence_id
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9
		)
		ON CONFLICT (calendar_id, uid, COALESCE(recurrence_id, '')) DO UPDATE SET
			etag = excluded.etag,
			data = excluded.data,
			component = excluded.component,
			start_at = excluded.start_at,
			end_at = excluded.end_at,
			recurrence_id = excluded.recurrence_id,
			updated_at = now()
	`, obj.ID, obj.CalendarID, obj.UID, obj.ETag, obj.Data, obj.Component, obj.StartAt, obj.EndAt, recIDStr)

	return err
}

func updateCalendarCTagInTx(ctx context.Context, tx pgx.Tx, calendarID string) error {
	ctag := uuid.Must(uuid.NewV7()).String()
	_, err := tx.Exec(ctx, `
		update calendars 
		set ctag = $1, updated_at = now() 
		where id::text = $2
	`, ctag, calendarID)
	return err
}

func recordChangeInTx(ctx context.Context, tx pgx.Tx, calendarID, uid string, deleted bool) error {
	var newSeq int64
	err := tx.QueryRow(ctx, `
		update calendars
		set sync_seq = sync_seq + 1,
		    sync_token = 'seq:' || (sync_seq + 1)
		where id::text = $1
		returning sync_seq
	`, calendarID).Scan(&newSeq)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		insert into calendar_changes(calendar_id, seq, uid, deleted)
		values ($1::uuid, $2, $3, $4)
	`, calendarID, newSeq, uid, deleted)
	return err
}
