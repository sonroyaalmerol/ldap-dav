package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) CreateCalendar(c storage.Calendar, ownerGroup string, description string) error {
	id := c.ID
	if id == "" {
		id = randID()
	}
	if err := os.MkdirAll(s.calObjectsDir(id), 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()

	color := c.Color
	if color == "" {
		color = "#3174ad" // Default blue color
	}

	meta := calMeta{
		ID:          id,
		OwnerUserID: c.OwnerUserID,
		OwnerGroup:  ownerGroup,
		URI:         c.URI,
		DisplayName: c.DisplayName,
		Description: description,
		Color:       color,
		CTag:        randID(),
		CreatedAt:   now,
		UpdatedAt:   now,
		SyncToken:   "seq:0",
		SyncSeq:     0,
	}
	if err := writeJSON(s.calMetaPath(id), &meta); err != nil {
		return err
	}
	// create empty changes.log
	if _, err := os.Stat(s.calChangesPath(id)); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(s.calChangesPath(id), nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteCalendar(ownerUserID, calURI string) error {
	pattern := s.calMetaPath("*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	for _, metaPath := range matches {
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			continue
		}
		if meta.OwnerUserID == ownerUserID && meta.URI == calURI {
			id := meta.ID
			if err := os.RemoveAll(s.calObjectsDir(id)); err != nil {
				return err
			}
			_ = os.Remove(s.calChangesPath(id))
			if err := os.Remove(metaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		}
	}

	return fs.ErrNotExist
}

func (s *Store) GetCalendarByID(ctx context.Context, id string) (*storage.Calendar, error) {
	var meta calMeta
	if err := readJSON(s.calMetaPath(id), &meta); err != nil {
		return nil, err
	}
	return &storage.Calendar{
		ID:          meta.ID,
		OwnerUserID: meta.OwnerUserID,
		OwnerGroup:  meta.OwnerGroup,
		URI:         meta.URI,
		DisplayName: meta.DisplayName,
		Description: meta.Description,
		Color:       meta.Color,
		CTag:        meta.CTag,
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   meta.UpdatedAt,
	}, nil
}

func (s *Store) UpdateCalendarDisplayName(ctx context.Context, ownerUID, calURI string, displayName *string) error {
	base := filepath.Join(s.root, "calendars")
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		metaPath := s.calMetaPath(id)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			continue
		}
		if meta.OwnerUserID == ownerUID && meta.URI == calURI {
			// ensure directory exists (optional)
			dir := s.calDir(id)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			return s.withCalLock(id, func() error {
				// reload to avoid TOCTOU
				if err := readJSON(metaPath, &meta); err != nil {
					return err
				}
				if displayName == nil {
					meta.DisplayName = ""
				} else {
					meta.DisplayName = *displayName
				}
				meta.UpdatedAt = time.Now().UTC()
				meta.CTag = randID()
				return writeJSON(metaPath, &meta)
			})
		}
	}
	return fs.ErrNotExist
}

func (s *Store) ListCalendarsByOwnerUser(ctx context.Context, uid string) ([]*storage.Calendar, error) {
	all, err := s.ListAllCalendars(ctx)
	if err != nil {
		return nil, err
	}
	var out []*storage.Calendar
	for _, c := range all {
		if c.OwnerUserID == uid {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Store) ListAllCalendars(ctx context.Context) ([]*storage.Calendar, error) {
	base := filepath.Join(s.root, "calendars")
	entries, err := os.ReadDir(base)
	if err != nil {
		// if dir not exists, return empty
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*storage.Calendar
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		var meta calMeta
		if err := readJSON(s.calMetaPath(id), &meta); err != nil {
			continue
		}
		out = append(out, &storage.Calendar{
			ID:          meta.ID,
			OwnerUserID: meta.OwnerUserID,
			OwnerGroup:  meta.OwnerGroup,
			URI:         meta.URI,
			DisplayName: meta.DisplayName,
			Description: meta.Description,
			Color:       meta.Color,
			CTag:        meta.CTag,
			CreatedAt:   meta.CreatedAt,
			UpdatedAt:   meta.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) UpdateCalendarColor(ctx context.Context, ownerUID, calURI, color string) error {
	base := filepath.Join(s.root, "calendars")
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		metaPath := s.calMetaPath(id)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			continue
		}
		if meta.OwnerUserID == ownerUID && meta.URI == calURI {
			// ensure directory exists (optional)
			dir := s.calDir(id)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			return s.withCalLock(id, func() error {
				// reload to avoid TOCTOU
				if err := readJSON(metaPath, &meta); err != nil {
					return err
				}
				meta.Color = color
				meta.UpdatedAt = time.Now().UTC()
				meta.CTag = randID()
				return writeJSON(metaPath, &meta)
			})
		}
	}
	return fs.ErrNotExist
}

func (s *Store) objPath(calendarID, uid string) string {
	// one file per UID
	filename := uid + ".json"
	return filepath.Join(s.calObjectsDir(calendarID), filename)
}

func (s *Store) GetObject(ctx context.Context, calendarID, uid string) (*storage.Object, error) {
	var of objFile
	if err := readJSON(s.objPath(calendarID, uid), &of); err != nil {
		return nil, err
	}
	return &storage.Object{
		ID:         of.ID,
		CalendarID: of.CalendarID,
		UID:        of.UID,
		ETag:       of.ETag,
		Data:       of.Data,
		Component:  of.Component,
		StartAt:    of.StartAt,
		EndAt:      of.EndAt,
		UpdatedAt:  of.UpdatedAt,
	}, nil
}

func (s *Store) PutObject(ctx context.Context, obj *storage.Object) error {
	if obj.CalendarID == "" || obj.UID == "" {
		return errors.New("calendarID and uid required")
	}
	id := obj.CalendarID
	return s.withCalLock(id, func() error {
		// ensure dirs
		if err := os.MkdirAll(s.calObjectsDir(id), 0o755); err != nil {
			return err
		}
		// load calendar meta
		metaPath := s.calMetaPath(id)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}

		// assign IDs/ETag
		if obj.ID == "" {
			obj.ID = randID()
		}
		if obj.ETag == "" {
			obj.ETag = randID()
		}
		obj.UpdatedAt = time.Now().UTC()

		of := objFile{
			ID:         obj.ID,
			CalendarID: obj.CalendarID,
			UID:        obj.UID,
			ETag:       obj.ETag,
			Data:       obj.Data,
			Component:  obj.Component,
			StartAt:    obj.StartAt,
			EndAt:      obj.EndAt,
			UpdatedAt:  obj.UpdatedAt,
		}

		if err := writeJSON(s.objPath(id, obj.UID), &of); err != nil {
			return err
		}

		// bump CTag
		meta.CTag = randID()
		meta.UpdatedAt = time.Now().UTC()
		if err := writeJSON(metaPath, &meta); err != nil {
			return err
		}

		// record change
		_, _, err := s.recordChangeLocked(&meta, id, obj.UID, false)
		return err
	})
}

func (s *Store) DeleteObject(ctx context.Context, calendarID, uid string, etag string) error {
	id := calendarID
	return s.withCalLock(id, func() error {
		metaPath := s.calMetaPath(id)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}

		objPath := s.objPath(id, uid)
		// if etag provided, verify
		if etag != "" {
			var of objFile
			if err := readJSON(objPath, &of); err != nil {
				return err
			}
			if of.ETag != etag {
				return fmt.Errorf("etag mismatch")
			}
		}
		if err := os.Remove(objPath); err != nil {
			return err
		}

		// bump CTag
		meta.CTag = randID()
		meta.UpdatedAt = time.Now().UTC()
		if err := writeJSON(metaPath, &meta); err != nil {
			return err
		}

		// record change as deleted
		_, _, err := s.recordChangeLocked(&meta, id, uid, true)
		return err
	})
}

func (s *Store) ListObjects(ctx context.Context, calendarID string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	return s.listObjectsFiltered(ctx, calendarID, nil, start, end)
}

func (s *Store) ListObjectsByComponent(ctx context.Context, calendarID string, components []string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	return s.listObjectsFiltered(ctx, calendarID, components, start, end)
}

func (s *Store) listObjectsFiltered(ctx context.Context, calendarID string, components []string, start *time.Time, end *time.Time) ([]*storage.Object, error) {
	dir := s.calObjectsDir(calendarID)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	hasComp := len(components) > 0
	compSet := map[string]struct{}{}
	for _, c := range components {
		compSet[strings.ToUpper(c)] = struct{}{}
	}
	var out []*storage.Object
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		var of objFile
		if err := readJSON(filepath.Join(dir, ent.Name()), &of); err != nil {
			continue
		}
		if hasComp {
			if _, ok := compSet[strings.ToUpper(of.Component)]; !ok {
				continue
			}
		}
		// time filters (same logic as SQL sample)
		if start != nil {
			// event overlaps [start, ...]
			if !(of.StartAt == nil || (of.EndAt != nil && !of.EndAt.Before(*start)) || (of.EndAt == nil)) {
				continue
			}
			// In SQL, they used: (start_at is null or end_at >= start)
			if of.StartAt != nil && of.EndAt != nil && of.EndAt.Before(*start) {
				continue
			}
		}
		if end != nil {
			// In SQL: (end_at is null or start_at <= end)
			if of.StartAt != nil && of.StartAt.After(*end) {
				continue
			}
		}
		out = append(out, &storage.Object{
			ID:         of.ID,
			CalendarID: of.CalendarID,
			UID:        of.UID,
			ETag:       of.ETag,
			Data:       of.Data,
			Component:  of.Component,
			StartAt:    of.StartAt,
			EndAt:      of.EndAt,
			UpdatedAt:  of.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) NewCTag(ctx context.Context, calendarID string) (string, error) {
	var newCTag string
	err := s.withCalLock(calendarID, func() error {
		metaPath := s.calMetaPath(calendarID)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}
		newCTag = randID()
		meta.CTag = newCTag
		meta.UpdatedAt = time.Now().UTC()
		return writeJSON(metaPath, &meta)
	})
	return newCTag, err
}

func (s *Store) GetSyncInfo(ctx context.Context, calendarID string) (string, int64, error) {
	var meta calMeta
	if err := readJSON(s.calMetaPath(calendarID), &meta); err != nil {
		return "", 0, err
	}
	return meta.SyncToken, meta.SyncSeq, nil
}

func (s *Store) ListChangesSince(ctx context.Context, calendarID string, sinceSeq int64, limit int) ([]storage.Change, int64, error) {
	f, err := os.Open(s.calChangesPath(calendarID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, sinceSeq, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	var out []storage.Change
	var last int64 = sinceSeq

	reader := io.Reader(f)
	dec := json.NewDecoder(reader)
	// changes.log is JSONL (one JSON object per line). json.Decoder reads continuous JSON values.
	for {
		var row changeRow
		if err := dec.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// If changes.log is JSONL, Decoder.Decode on concatenated objects works.
			// If a malformed line appears, stop.
			return out, last, nil
		}
		if row.Seq > sinceSeq {
			out = append(out, storage.Change{
				UID:     row.UID,
				Deleted: row.Deleted,
				Seq:     row.Seq,
			})
			last = row.Seq
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, last, nil
}

func (s *Store) RecordChange(ctx context.Context, calendarID, uid string, deleted bool) (string, int64, error) {
	var token string
	var seq int64
	err := s.withCalLock(calendarID, func() error {
		metaPath := s.calMetaPath(calendarID)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}
		tok, newSeq, err := s.recordChangeLocked(&meta, calendarID, uid, deleted)
		if err != nil {
			return err
		}
		token = tok
		seq = newSeq
		return writeJSON(metaPath, &meta)
	})
	return token, seq, err
}

// recordChangeLocked increments seq, sets sync_token, appends change row.
// Caller must hold calendar lock and pass the loaded meta; meta is updated in-place.
func (s *Store) recordChangeLocked(meta *calMeta, calendarID, uid string, deleted bool) (string, int64, error) {
	// load seq
	seq := meta.SyncSeq
	seq++
	meta.SyncSeq = seq
	meta.SyncToken = "seq:" + strconv.FormatInt(seq, 10)

	// persist seq.txt (optional, for visibility)
	if err := os.WriteFile(s.calSeqPath(calendarID), []byte(strconv.FormatInt(seq, 10)), 0o644); err != nil {
		return "", 0, err
	}
	if err := os.WriteFile(s.calCTagPath(calendarID), []byte(meta.CTag), 0o644); err != nil {
		// best-effort; not critical
		_ = err
	}

	// append change
	row := changeRow{Seq: seq, UID: uid, Deleted: deleted}
	if err := appendJSONLines(s.calChangesPath(calendarID), row); err != nil {
		return "", 0, err
	}
	return meta.SyncToken, seq, nil
}
