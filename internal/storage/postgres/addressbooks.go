package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) CreateAddressbook(a storage.Addressbook, ownerGroup string, description string) error {
	if a.ID == "" {
		a.ID = uuid.Must(uuid.NewV7()).String()
	}
	if a.CTag == "" {
		a.CTag = uuid.Must(uuid.NewV7()).String()
	}

	_, err := s.pool.Exec(context.Background(), `
		insert into addressbooks (
			id, owner_user_id, owner_group, uri, display_name, description, ctag, sync_seq, sync_token
		) values (
			$1::uuid, $2, $3, $4, $5, $6, $7, 0, 'seq:0'
		)
	`, a.ID, a.OwnerUserID, ownerGroup, a.URI, a.DisplayName, description, a.CTag)
	return err
}

func (s *Store) DeleteAddressbook(ownerUserID, abURI string) error {
	_, err := s.pool.Exec(context.Background(), `
		delete from addressbooks where owner_user_id = $1 and uri = $2
	`, ownerUserID, abURI)
	return err
}

func (s *Store) GetAddressbookByURI(ctx context.Context, uri string) (*storage.Addressbook, error) {
	row := s.pool.QueryRow(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
        from addressbooks where uri = $1`, uri)
	var a storage.Addressbook
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) UpdateAddressbookDisplayName(ctx context.Context, ownerUID, abURI string, displayName *string) error {
	_, err := s.pool.Exec(ctx, `
		update addressbooks
		set display_name = $1, updated_at = now()
		where owner_user_id = $2 and uri = $3
	`, displayName, ownerUID, abURI)
	return err
}

func (s *Store) ListAddressbooksByOwnerUser(ctx context.Context, uid string) ([]*storage.Addressbook, error) {
	rows, err := s.pool.Query(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
        from addressbooks where owner_user_id = $1`, uid)
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
	rows, err := s.pool.Query(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, ctag, created_at, updated_at
        from addressbooks`)
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
	row := s.pool.QueryRow(ctx, `
		select id::text, addressbook_id::text, uid, etag, data, updated_at
		from contacts where addressbook_id::text = $1 and uid = $2`, addressbookID, uid)
	var c storage.Contact
	if err := row.Scan(&c.ID, &c.AddressbookID, &c.UID, &c.ETag, &c.Data, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) PutContact(ctx context.Context, c *storage.Contact) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if c.ID == "" {
		c.ID = uuid.Must(uuid.NewV7()).String()
	}
	// Always generate a new ETag
	c.ETag = uuid.Must(uuid.NewV7()).String()

	_, err = tx.Exec(ctx, `
		insert into contacts (
			id, addressbook_id, uid, etag, data
		) values (
			$1::uuid, $2::uuid, $3, $4, $5
		)
		on conflict (addressbook_id, uid) do update set
			etag = excluded.etag,
			data = excluded.data,
			updated_at = now()
	`, c.ID, c.AddressbookID, c.UID, c.ETag, c.Data)
	if err != nil {
		return err
	}

	// Update addressbook CTag and sync info
	if err := updateAddressbookCTagInTx(ctx, tx, c.AddressbookID); err != nil {
		return err
	}

	// Record change for sync
	if err := recordAddressbookChangeInTx(ctx, tx, c.AddressbookID, c.UID, false); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) DeleteContact(ctx context.Context, addressbookID, uid string, etag string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var cmdTag pgconn.CommandTag
	if etag == "" {
		cmdTag, err = tx.Exec(ctx, `
			delete from contacts where addressbook_id::text = $1 and uid = $2
		`, addressbookID, uid)
	} else {
		cmdTag, err = tx.Exec(ctx, `
			delete from contacts where addressbook_id::text = $1 and uid = $2 and etag = $3
		`, addressbookID, uid, etag)
	}

	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() == 0 {
		if etag != "" {
			// Check if contact exists
			var exists bool
			err := tx.QueryRow(ctx, `select exists(select 1 from contacts where addressbook_id::text = $1 and uid = $2)`, addressbookID, uid).Scan(&exists)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("etag mismatch")
			}
		}
		return sql.ErrNoRows
	}

	// Update addressbook CTag and sync info
	if err := updateAddressbookCTagInTx(ctx, tx, addressbookID); err != nil {
		return err
	}

	// Record change for sync
	if err := recordAddressbookChangeInTx(ctx, tx, addressbookID, uid, true); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) ListContacts(ctx context.Context, addressbookID string) ([]*storage.Contact, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, addressbook_id::text, uid, etag, data, updated_at
		from contacts
		where addressbook_id::text = $1`, addressbookID)
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
		select id::text, addressbook_id::text, uid, etag, data, updated_at
		from contacts
		where addressbook_id::text = $1`
	args := []any{addressbookID}

	if len(propNames) > 0 {
		for i, prop := range propNames {
			q += fmt.Sprintf(" and data ilike $%d", i+2)
			args = append(args, "%"+prop+"%")
		}
	}

	rows, err := s.pool.Query(ctx, q, args...)
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
	row := s.pool.QueryRow(ctx, `
		select sync_token, sync_seq from addressbooks where id::text = $1
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
		select seq, uid, deleted
		from addressbook_changes
		where addressbook_id::text = $1 and seq > $2
		order by seq asc`
	args := []any{addressbookID, sinceSeq}
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

// Helper function to update CTag within a transaction
func updateAddressbookCTagInTx(ctx context.Context, tx pgx.Tx, addressbookID string) error {
	ctag := uuid.Must(uuid.NewV7()).String()
	_, err := tx.Exec(ctx, `
		update addressbooks 
		set ctag = $1, updated_at = now() 
		where id::text = $2
	`, ctag, addressbookID)
	return err
}

// Helper function to record change within a transaction
func recordAddressbookChangeInTx(ctx context.Context, tx pgx.Tx, addressbookID, uid string, deleted bool) error {
	// Increment seq and get new value
	var newSeq int64
	err := tx.QueryRow(ctx, `
		update addressbooks
		set sync_seq = sync_seq + 1,
		    sync_token = 'seq:' || (sync_seq + 1)
		where id::text = $1
		returning sync_seq
	`, addressbookID).Scan(&newSeq)
	if err != nil {
		return err
	}

	// Insert change row
	_, err = tx.Exec(ctx, `
		insert into addressbook_changes(addressbook_id, seq, uid, deleted)
		values ($1::uuid, $2, $3, $4)
	`, addressbookID, newSeq, uid, deleted)
	return err
}
