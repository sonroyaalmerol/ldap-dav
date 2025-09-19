package filestore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

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
