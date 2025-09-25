package postgres

import (
	"context"
	"fmt"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) CreateAddressbook(a storage.Addressbook, ownerGroup string, description string) error {
	if a.ID == "" {
		a.ID = randID()
	}
	if a.CTag == "" {
		a.CTag = randID()
	}

	_, err := s.pool.Exec(context.Background(), `
		insert into addressbooks (
			id, owner_user_id, owner_group, uri, display_name, description, color, ctag
		) values (
			$1::uuid, $2, $3, $4, $5, $6, $7, $8
		)
	`, a.ID, a.OwnerUserID, ownerGroup, a.URI, a.DisplayName, description, a.Color, a.CTag)
	return err
}

func (s *Store) DeleteAddressbook(ownerUserID, abURI string) error {
	_, err := s.pool.Exec(context.Background(), `
		delete from addressbooks where owner_user_id = $1 and uri = $2
	`, ownerUserID, abURI)
	return err
}

func (s *Store) GetAddressbookByID(ctx context.Context, id string) (*storage.Addressbook, error) {
	row := s.pool.QueryRow(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        from addressbooks where id::text = $1`, id)
	var a storage.Addressbook
	if err := row.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.Color, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
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

func (s *Store) UpdateAddressbookColor(ctx context.Context, ownerUID, abURI, color string) error {
	_, err := s.pool.Exec(ctx, `
        update addressbooks
        set color = $1, updated_at = now()
        where owner_user_id = $2 and uri = $3
    `, color, ownerUID, abURI)
	return err
}

func (s *Store) ListAddressbooksByOwnerUser(ctx context.Context, uid string) ([]*storage.Addressbook, error) {
	rows, err := s.pool.Query(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        from addressbooks where owner_user_id = $1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Addressbook
	for rows.Next() {
		var a storage.Addressbook
		if err := rows.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.Color, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, nil
}

func (s *Store) ListAllAddressbooks(ctx context.Context) ([]*storage.Addressbook, error) {
	rows, err := s.pool.Query(ctx, `
        select id::text, owner_user_id, owner_group, uri, display_name, description, color, ctag, created_at, updated_at
        from addressbooks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*storage.Addressbook
	for rows.Next() {
		var a storage.Addressbook
		if err := rows.Scan(&a.ID, &a.OwnerUserID, &a.OwnerGroup, &a.URI, &a.DisplayName, &a.Description, &a.Color, &a.CTag, &a.CreatedAt, &a.UpdatedAt); err != nil {
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
	if c.ID == "" {
		c.ID = randID()
	}
	if c.ETag == "" {
		c.ETag = randID()
	}
	_, err := s.pool.Exec(ctx, `
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
	return err
}

func (s *Store) DeleteContact(ctx context.Context, addressbookID, uid string, etag string) error {
	if etag == "" {
		_, err := s.pool.Exec(ctx, `
			delete from contacts where addressbook_id::text = $1 and uid = $2
		`, addressbookID, uid)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		delete from contacts where addressbook_id::text = $1 and uid = $2 and etag = $3
	`, addressbookID, uid, etag)
	return err
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

func (s *Store) NewAddressbookCTag(ctx context.Context, addressbookID string) (string, error) {
	ctag := randID()
	_, err := s.pool.Exec(ctx, `update addressbooks set ctag = $1, updated_at = now() where id::text = $2`, ctag, addressbookID)
	return ctag, err
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
	if limit > 0 {
		q += " limit $3"
		rows, err := s.pool.Query(ctx, q, addressbookID, sinceSeq, limit)
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
	rows, err := s.pool.Query(ctx, q, addressbookID, sinceSeq)
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

func (s *Store) RecordAddressbookChange(ctx context.Context, addressbookID, uid string, deleted bool) (string, int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// increment seq and get new values
	var newSeq int64
	err = tx.QueryRow(ctx, `
		update addressbooks
		set sync_seq = sync_seq + 1,
		    sync_token = 'seq:' || (sync_seq + 1)
		where id::text = $1
		returning sync_seq, sync_token
	`, addressbookID).Scan(&newSeq, new(string)) // temporary placeholder
	if err != nil {
		return "", 0, err
	}

	var newToken string
	err = tx.QueryRow(ctx, `select sync_token from addressbooks where id::text = $1`, addressbookID).Scan(&newToken)
	if err != nil {
		return "", 0, err
	}

	// insert change row
	_, err = tx.Exec(ctx, `
		insert into addressbook_changes(addressbook_id, seq, uid, deleted)
		values ($1::uuid, $2, $3, $4)
	`, addressbookID, newSeq, uid, deleted)
	if err != nil {
		return "", 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", 0, err
	}
	return newToken, newSeq, nil
}
