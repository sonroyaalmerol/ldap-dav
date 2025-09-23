package filestore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

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
