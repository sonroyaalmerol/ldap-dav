package filestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
		ID:             id,
		OwnerUserID:    c.OwnerUserID,
		OwnerGroup:     ownerGroup,
		URI:            c.URI,
		DisplayName:    c.DisplayName,
		Description:    description,
		Color:          color,
		CTag:           randID(),
		CreatedAt:      now,
		UpdatedAt:      now,
		SyncToken:      "seq:0",
		SyncSeq:        0,
		ScheduleTransp: "opaque",
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
