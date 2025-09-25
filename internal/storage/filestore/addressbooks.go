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

func (s *Store) CreateAddressbook(a storage.Addressbook, ownerGroup string, description string) error {
	id := a.ID
	if id == "" {
		id = randID()
	}
	if err := os.MkdirAll(s.addressbookContactsDir(id), 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()

	meta := addressbookMeta{
		ID:          id,
		OwnerUserID: a.OwnerUserID,
		OwnerGroup:  ownerGroup,
		URI:         a.URI,
		DisplayName: a.DisplayName,
		Description: description,
		CTag:        randID(),
		CreatedAt:   now,
		UpdatedAt:   now,
		SyncToken:   "seq:0",
		SyncSeq:     0,
	}
	if err := writeJSON(s.addressbookMetaPath(id), &meta); err != nil {
		return err
	}
	// create empty changes.log
	if _, err := os.Stat(s.addressbookChangesPath(id)); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(s.addressbookChangesPath(id), nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteAddressbook(ownerUserID, abURI string) error {
	pattern := s.addressbookMetaPath("*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	for _, metaPath := range matches {
		var meta addressbookMeta
		if err := readJSON(metaPath, &meta); err != nil {
			continue
		}
		if meta.OwnerUserID == ownerUserID && meta.URI == abURI {
			id := meta.ID
			if err := os.RemoveAll(s.addressbookContactsDir(id)); err != nil {
				return err
			}
			_ = os.Remove(s.addressbookChangesPath(id))
			if err := os.Remove(metaPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		}
	}

	return fs.ErrNotExist
}

func (s *Store) GetAddressbookByID(ctx context.Context, id string) (*storage.Addressbook, error) {
	var meta addressbookMeta
	if err := readJSON(s.addressbookMetaPath(id), &meta); err != nil {
		return nil, err
	}
	return &storage.Addressbook{
		ID:          meta.ID,
		OwnerUserID: meta.OwnerUserID,
		OwnerGroup:  meta.OwnerGroup,
		URI:         meta.URI,
		DisplayName: meta.DisplayName,
		Description: meta.Description,
		CTag:        meta.CTag,
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   meta.UpdatedAt,
	}, nil
}

func (s *Store) UpdateAddressbookDisplayName(ctx context.Context, ownerUID, abURI string, displayName *string) error {
	base := filepath.Join(s.root, "addressbooks")
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		metaPath := s.addressbookMetaPath(id)
		var meta addressbookMeta
		if err := readJSON(metaPath, &meta); err != nil {
			continue
		}
		if meta.OwnerUserID == ownerUID && meta.URI == abURI {
			// ensure directory exists (optional)
			dir := s.addressbookDir(id)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			return s.withAddressbookLock(id, func() error {
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

func (s *Store) UpdateAddressbookColor(ctx context.Context, ownerUID, abURI, color string) error {
	base := filepath.Join(s.root, "addressbooks")
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		metaPath := s.addressbookMetaPath(id)
		var meta addressbookMeta
		if err := readJSON(metaPath, &meta); err != nil {
			continue
		}
		if meta.OwnerUserID == ownerUID && meta.URI == abURI {
			// ensure directory exists (optional)
			dir := s.addressbookDir(id)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			return s.withAddressbookLock(id, func() error {
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

func (s *Store) ListAddressbooksByOwnerUser(ctx context.Context, uid string) ([]*storage.Addressbook, error) {
	all, err := s.ListAllAddressbooks(ctx)
	if err != nil {
		return nil, err
	}
	var out []*storage.Addressbook
	for _, a := range all {
		if a.OwnerUserID == uid {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Store) ListAllAddressbooks(ctx context.Context) ([]*storage.Addressbook, error) {
	base := filepath.Join(s.root, "addressbooks")
	entries, err := os.ReadDir(base)
	if err != nil {
		// if dir not exists, return empty
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*storage.Addressbook
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		var meta addressbookMeta
		if err := readJSON(s.addressbookMetaPath(id), &meta); err != nil {
			continue
		}
		out = append(out, &storage.Addressbook{
			ID:          meta.ID,
			OwnerUserID: meta.OwnerUserID,
			OwnerGroup:  meta.OwnerGroup,
			URI:         meta.URI,
			DisplayName: meta.DisplayName,
			Description: meta.Description,
			CTag:        meta.CTag,
			CreatedAt:   meta.CreatedAt,
			UpdatedAt:   meta.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) contactPath(addressbookID, uid string) string {
	// one file per UID
	filename := uid + ".json"
	return filepath.Join(s.addressbookContactsDir(addressbookID), filename)
}

func (s *Store) GetContact(ctx context.Context, addressbookID, uid string) (*storage.Contact, error) {
	var cf contactFile
	if err := readJSON(s.contactPath(addressbookID, uid), &cf); err != nil {
		return nil, err
	}
	return &storage.Contact{
		ID:            cf.ID,
		AddressbookID: cf.AddressbookID,
		UID:           cf.UID,
		ETag:          cf.ETag,
		Data:          cf.Data,
		UpdatedAt:     cf.UpdatedAt,
	}, nil
}

func (s *Store) PutContact(ctx context.Context, c *storage.Contact) error {
	if c.AddressbookID == "" || c.UID == "" {
		return errors.New("addressbookID and uid required")
	}
	id := c.AddressbookID
	return s.withAddressbookLock(id, func() error {
		// ensure dirs
		if err := os.MkdirAll(s.addressbookContactsDir(id), 0o755); err != nil {
			return err
		}
		// load addressbook meta
		metaPath := s.addressbookMetaPath(id)
		var meta addressbookMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}

		// assign IDs/ETag
		if c.ID == "" {
			c.ID = randID()
		}
		if c.ETag == "" {
			c.ETag = randID()
		}
		c.UpdatedAt = time.Now().UTC()

		cf := contactFile{
			ID:            c.ID,
			AddressbookID: c.AddressbookID,
			UID:           c.UID,
			ETag:          c.ETag,
			Data:          c.Data,
			UpdatedAt:     c.UpdatedAt,
		}

		if err := writeJSON(s.contactPath(id, c.UID), &cf); err != nil {
			return err
		}

		// bump CTag
		meta.CTag = randID()
		meta.UpdatedAt = time.Now().UTC()
		if err := writeJSON(metaPath, &meta); err != nil {
			return err
		}

		// record change
		_, _, err := s.recordAddressbookChangeLocked(&meta, id, c.UID, false)
		return err
	})
}

func (s *Store) DeleteContact(ctx context.Context, addressbookID, uid string, etag string) error {
	id := addressbookID
	return s.withAddressbookLock(id, func() error {
		metaPath := s.addressbookMetaPath(id)
		var meta addressbookMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}

		contactPath := s.contactPath(id, uid)
		// if etag provided, verify
		if etag != "" {
			var cf contactFile
			if err := readJSON(contactPath, &cf); err != nil {
				return err
			}
			if cf.ETag != etag {
				return fmt.Errorf("etag mismatch")
			}
		}
		if err := os.Remove(contactPath); err != nil {
			return err
		}

		// bump CTag
		meta.CTag = randID()
		meta.UpdatedAt = time.Now().UTC()
		if err := writeJSON(metaPath, &meta); err != nil {
			return err
		}

		// record change as deleted
		_, _, err := s.recordAddressbookChangeLocked(&meta, id, uid, true)
		return err
	})
}

func (s *Store) ListContacts(ctx context.Context, addressbookID string) ([]*storage.Contact, error) {
	return s.listContactsFiltered(ctx, addressbookID, nil)
}

func (s *Store) ListContactsByFilter(ctx context.Context, addressbookID string, propNames []string) ([]*storage.Contact, error) {
	return s.listContactsFiltered(ctx, addressbookID, propNames)
}

func (s *Store) listContactsFiltered(ctx context.Context, addressbookID string, propNames []string) ([]*storage.Contact, error) {
	dir := s.addressbookContactsDir(addressbookID)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var out []*storage.Contact
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		var cf contactFile
		if err := readJSON(filepath.Join(dir, ent.Name()), &cf); err != nil {
			continue
		}

		// Simple property filter - check if any of the property names exist in the vCard data
		if len(propNames) > 0 {
			found := false
			for _, prop := range propNames {
				if strings.Contains(strings.ToUpper(cf.Data), strings.ToUpper(prop)) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		out = append(out, &storage.Contact{
			ID:            cf.ID,
			AddressbookID: cf.AddressbookID,
			UID:           cf.UID,
			ETag:          cf.ETag,
			Data:          cf.Data,
			UpdatedAt:     cf.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) NewAddressbookCTag(ctx context.Context, addressbookID string) (string, error) {
	var newCTag string
	err := s.withAddressbookLock(addressbookID, func() error {
		metaPath := s.addressbookMetaPath(addressbookID)
		var meta addressbookMeta
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

func (s *Store) GetAddressbookSyncInfo(ctx context.Context, addressbookID string) (string, int64, error) {
	var meta addressbookMeta
	if err := readJSON(s.addressbookMetaPath(addressbookID), &meta); err != nil {
		return "", 0, err
	}
	return meta.SyncToken, meta.SyncSeq, nil
}

func (s *Store) ListAddressbookChangesSince(ctx context.Context, addressbookID string, sinceSeq int64, limit int) ([]storage.Change, int64, error) {
	f, err := os.Open(s.addressbookChangesPath(addressbookID))
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

func (s *Store) RecordAddressbookChange(ctx context.Context, addressbookID, uid string, deleted bool) (string, int64, error) {
	var token string
	var seq int64
	err := s.withAddressbookLock(addressbookID, func() error {
		metaPath := s.addressbookMetaPath(addressbookID)
		var meta addressbookMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}
		tok, newSeq, err := s.recordAddressbookChangeLocked(&meta, addressbookID, uid, deleted)
		if err != nil {
			return err
		}
		token = tok
		seq = newSeq
		return writeJSON(metaPath, &meta)
	})
	return token, seq, err
}

// recordAddressbookChangeLocked increments seq, sets sync_token, appends change row.
// Caller must hold addressbook lock and pass the loaded meta; meta is updated in-place.
func (s *Store) recordAddressbookChangeLocked(meta *addressbookMeta, addressbookID, uid string, deleted bool) (string, int64, error) {
	// load seq
	seq := meta.SyncSeq
	seq++
	meta.SyncSeq = seq
	meta.SyncToken = "seq:" + strconv.FormatInt(seq, 10)

	// persist seq.txt (optional, for visibility)
	if err := os.WriteFile(s.addressbookSeqPath(addressbookID), []byte(strconv.FormatInt(seq, 10)), 0o644); err != nil {
		return "", 0, err
	}
	if err := os.WriteFile(s.addressbookCTagPath(addressbookID), []byte(meta.CTag), 0o644); err != nil {
		// best-effort; not critical
		_ = err
	}

	// append change
	row := changeRow{Seq: seq, UID: uid, Deleted: deleted}
	if err := appendJSONLines(s.addressbookChangesPath(addressbookID), row); err != nil {
		return "", 0, err
	}
	return meta.SyncToken, seq, nil
}
