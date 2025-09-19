package filestore

import (
	"errors"
	"io/fs"
	"os"
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
	meta := calMeta{
		ID:          id,
		OwnerUserID: c.OwnerUserID,
		OwnerGroup:  ownerGroup,
		URI:         c.URI,
		DisplayName: c.DisplayName,
		Description: description,
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
