package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Store struct {
	pool   *pgxpool.Pool
	logger zerolog.Logger
}

func New(dsn string, logger zerolog.Logger) (*Store, error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool, logger: logger}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) GetCalendarByID(ctx context.Context, id string) (*storage.Calendar, error) {
	row := s.pool.QueryRow(ctx, `
		select id::text, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
		from calendars where id::text = $1`, id)
	var c storage.Calendar
	if err := row.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
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
		select id::text, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
		from calendars where owner_user_id = $1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Calendar
	for rows.Next() {
		var c storage.Calendar
		if err := rows.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *Store) ListAllCalendars(ctx context.Context) ([]*storage.Calendar, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
		from calendars`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Calendar
	for rows.Next() {
		var c storage.Calendar
		if err := rows.Scan(&c.ID, &c.OwnerUserID, &c.OwnerGroup, &c.URI, &c.DisplayName, &c.Description, &c.CTag, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *Store) GetObject(ctx context.Context, calendarID, uid string) (*storage.Object, error) {
	row := s.pool.QueryRow(ctx, `
		select id::text, calendar_id::text, uid, etag, data, component, start_at, end_at, updated_at
		from calendar_objects where calendar_id::text = $1 and uid = $2`, calendarID, uid)
	var o storage.Object
	if err := row.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) PutObject(ctx context.Context, obj *storage.Object) error {
	if obj.ID == "" {
		obj.ID = randID()
	}
	if obj.ETag == "" {
		obj.ETag = randID()
	}
	_, err := s.pool.Exec(ctx, `
		insert into calendar_objects(id, calendar_id, uid, etag, data, component, start_at, end_at, updated_at)
		values ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, now())
		on conflict (calendar_id, uid) do update set
			etag = excluded.etag, data = excluded.data, component = excluded.component,
			start_at = excluded.start_at, end_at = excluded.end_at, updated_at = now()
	`, obj.ID, obj.CalendarID, obj.UID, obj.ETag, obj.Data, obj.Component, obj.StartAt, obj.EndAt)
	return err
}

func (s *Store) DeleteObject(ctx context.Context, calendarID, uid, etag string) error {
	if etag == "" {
		_, err := s.pool.Exec(ctx, `
			delete from calendar_objects where calendar_id::text = $1 and uid = $2
		`, calendarID, uid)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		delete from calendar_objects where calendar_id::text = $1 and uid = $2 and etag = $3
	`, calendarID, uid, etag)
	return err
}

func (s *Store) ListObjects(ctx context.Context, calendarID string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	q := `
		select id::text, calendar_id::text, uid, etag, data, component, start_at, end_at, updated_at
		from calendar_objects
		where calendar_id::text = $1`
	args := []any{calendarID}
	if start != nil {
		q += " and (start_at is null or end_at >= $2)"
		args = append(args, *start)
		if end != nil {
			q += " and (end_at is null or start_at <= $3)"
			args = append(args, *end)
		}
	} else if end != nil {
		q += " and (start_at is null or start_at <= $2)"
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
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *Store) NewCTag(ctx context.Context, calendarID string) (string, error) {
	ctag := randID()
	_, err := s.pool.Exec(ctx, `update calendars set ctag = $1, updated_at = now() where id::text = $2`, ctag, calendarID)
	return ctag, err
}

func randID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) ListObjectsByComponent(ctx context.Context, calendarID string, components []string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	q := `
		select id::text, calendar_id::text, uid, etag, data, component, start_at, end_at, updated_at
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
		if err := rows.Scan(&o.ID, &o.CalendarID, &o.UID, &o.ETag, &o.Data, &o.Component, &o.StartAt, &o.EndAt, &o.UpdatedAt); err != nil {
			return nil, err
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
	if limit > 0 {
		q += " limit $3"
		rows, err := s.pool.Query(ctx, q, calendarID, sinceSeq, limit)
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
	rows, err := s.pool.Query(ctx, q, calendarID, sinceSeq)
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// increment seq and get new values
	var newSeq int64
	err = tx.QueryRow(ctx, `
		update calendars
		set sync_seq = sync_seq + 1,
		    sync_token = 'seq:' || (sync_seq + 1)
		where id::text = $1
		returning sync_seq, sync_token
	`, calendarID).Scan(&newSeq, new(string)) // temporary placeholder
	if err != nil {
		return "", 0, err
	}

	var newToken string
	err = tx.QueryRow(ctx, `select sync_token from calendars where id::text = $1`, calendarID).Scan(&newToken)
	if err != nil {
		return "", 0, err
	}

	// insert change row
	_, err = tx.Exec(ctx, `
		insert into calendar_changes(calendar_id, seq, uid, deleted)
		values ($1::uuid, $2, $3, $4)
	`, calendarID, newSeq, uid, deleted)
	if err != nil {
		return "", 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return newToken, newSeq, nil
}
