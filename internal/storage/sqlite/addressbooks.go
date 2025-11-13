package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) CreateAddressbook(a storage.Addressbook, ownerGroup string, description string) error {
	return s.withTx(context.Background(), func(tx *sql.Tx) error {
		if a.ID == "" {
			a.ID = uuid.Must(uuid.NewV7()).String()
		}
		if a.CTag == "" {
			a.CTag = uuid.Must(uuid.NewV7()).String()
		}

		_, err := tx.Exec(`
			INSERT INTO addressbooks (
				id, owner_user_id, owner_group, uri, display_name, description, ctag, sync_seq, sync_token
			) VALUES (
				?, ?, ?, ?, ?, ?, ?, 0, 'seq:0'
			)
		`, a.ID, a.OwnerUserID, ownerGroup, a.URI, a.DisplayName, description, a.CTag)
		return err
	})
}

func (s *Store) DeleteAddressbook(ownerUserID, abURI string) error {
	_, err := s.db.ExecContext(context.Background(), `
		DELETE FROM addressbooks WHERE owner_user_id = ? AND uri = ?
	`, ownerUserID, abURI)
	return err
}

func (s *Store) GetAddressbookByURI(ctx context.Context, uri string) (*storage.Addressbook, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
        FROM addressbooks WHERE uri = ?`, uri)
	var a storage.Addressbook
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) UpdateAddressbookDisplayName(ctx context.Context, ownerUID, abURI string, displayName *string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE addressbooks
		SET display_name = ?, updated_at = datetime('now')
		WHERE owner_user_id = ? AND uri = ?
	`, displayName, ownerUID, abURI)
	return err
}

func (s *Store) ListAddressbooksByOwnerUser(ctx context.Context, uid string) ([]*storage.Addressbook, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
        FROM addressbooks WHERE owner_user_id = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Addressbook
	for rows.Next() {
		var a storage.Addressbook
		if err := rows.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, nil
}

func (s *Store) ListAllAddressbooks(ctx context.Context) ([]*storage.Addressbook, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
        FROM addressbooks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Addressbook
	for rows.Next() {
		var a storage.Addressbook
		if err := rows.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, nil
}

func (s *Store) GetContact(ctx context.Context, addressbookID, uid string) (*storage.Contact, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, addressbook_id, uid, etag, data, updated_at
		FROM contacts WHERE addressbook_id = ? AND uid = ?`, addressbookID, uid)
	var c storage.Contact
	if err := row.Scan(&c.ID, &c.AddressbookID, &c.UID, &c.ETag, &c.Data, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) PutContact(ctx context.Context, c *storage.Contact) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if c.ID == "" {
			c.ID = uuid.Must(uuid.NewV7()).String()
		}
		c.ETag = uuid.Must(uuid.NewV7()).String()

		_, err := tx.Exec(`
			INSERT INTO contacts (
				id, addressbook_id, uid, etag, data
			) VALUES (
				?, ?, ?, ?, ?
			)
			ON CONFLICT (addressbook_id, uid) DO UPDATE SET
				etag = excluded.etag,
				data = excluded.data,
				updated_at = datetime('now')
		`, c.ID, c.AddressbookID, c.UID, c.ETag, c.Data)
		if err != nil {
			return err
		}

		// Update addressbook CTag and sync info
		if err := s.updateAddressbookCTagInTx(tx, c.AddressbookID); err != nil {
			return err
		}

		// Record change for sync
		if err := s.recordAddressbookChangeInTx(tx, c.AddressbookID, c.UID, false); err != nil {
			return err
		}

		return nil
	})
}

func (s *Store) DeleteContact(ctx context.Context, addressbookID, uid string, etag string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var result sql.Result
		var err error

		if etag == "" {
			result, err = tx.Exec(`
				DELETE FROM contacts WHERE addressbook_id = ? AND uid = ?
			`, addressbookID, uid)
		} else {
			result, err = tx.Exec(`
				DELETE FROM contacts WHERE addressbook_id = ? AND uid = ? AND etag = ?
			`, addressbookID, uid, etag)
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
				err := tx.QueryRow(`SELECT etag FROM contacts WHERE addressbook_id = ? AND uid = ?`, addressbookID, uid).Scan(&actualEtag)
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

		// Update addressbook CTag and sync info
		if err := s.updateAddressbookCTagInTx(tx, addressbookID); err != nil {
			return err
		}

		// Record change for sync
		if err := s.recordAddressbookChangeInTx(tx, addressbookID, uid, true); err != nil {
			return err
		}

		return nil
	})
}

func (s *Store) ListContacts(ctx context.Context, addressbookID string) ([]*storage.Contact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, addressbook_id, uid, etag, data, updated_at
		FROM contacts
		WHERE addressbook_id = ?`, addressbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.Contact
	for rows.Next() {
		var c storage.Contact
		if err := rows.Scan(&c.ID, &c.AddressbookID, &c.UID, &c.ETag, &c.Data, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *Store) ListContactsByFilter(ctx context.Context, addressbookID string, propNames []string) ([]*storage.Contact, error) {
	q := `
		SELECT id, addressbook_id, uid, etag, data, updated_at
		FROM contacts
		WHERE addressbook_id = ?`
	args := []interface{}{addressbookID}

	if len(propNames) > 0 {
		for _, prop := range propNames {
			q += " AND data LIKE ?"
			args = append(args, "%"+prop+"%")
		}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*storage.Contact
	for rows.Next() {
		var c storage.Contact
		if err := rows.Scan(&c.ID, &c.AddressbookID, &c.UID, &c.ETag, &c.Data, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, nil
}

func (s *Store) GetAddressbookSyncInfo(ctx context.Context, addressbookID string) (string, int64, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT sync_token, sync_seq FROM addressbooks WHERE id = ?
	`, addressbookID)
	var token string
	var seq int64
	if err := row.Scan(&token, &seq); err != nil {
		return "", 0, err
	}
	return token, seq, nil
}

func (s *Store) ListAddressbookChangesSince(ctx context.Context, addressbookID string, sinceSeq int64, limit int) ([]storage.Change, int64, error) {
	q := `
		SELECT seq, uid, deleted
		FROM addressbook_changes
		WHERE addressbook_id = ? AND seq > ?
		ORDER BY seq ASC`
	args := []interface{}{addressbookID, sinceSeq}
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

// Helper function to update CTag within a transaction
func (s *Store) updateAddressbookCTagInTx(tx *sql.Tx, addressbookID string) error {
	ctag := uuid.Must(uuid.NewV7()).String()
	_, err := tx.Exec(`
		UPDATE addressbooks 
		SET ctag = ?, updated_at = datetime('now') 
		WHERE id = ?
	`, ctag, addressbookID)
	return err
}

// Helper function to record change within a transaction
func (s *Store) recordAddressbookChangeInTx(tx *sql.Tx, addressbookID, uid string, deleted bool) error {
	// Increment seq and get new value
	var newSeq int64
	err := tx.QueryRow(`
		UPDATE addressbooks
		SET sync_seq = sync_seq + 1,
			sync_token = 'seq:' || (sync_seq + 1)
		WHERE id = ?
		RETURNING sync_seq
	`, addressbookID).Scan(&newSeq)
	if err != nil {
		return err
	}

	// Insert change row
	_, err = tx.Exec(`
		INSERT INTO addressbook_changes(addressbook_id, seq, uid, deleted)
		VALUES (?, ?, ?, ?)
	`, addressbookID, newSeq, uid, deleted)
	return err
}
