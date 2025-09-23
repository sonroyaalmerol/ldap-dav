package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
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
		color = "#3174ad" // Default blue color
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
