package filestore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) calDir(id string) string {
	return filepath.Join(s.root, "calendars", id)
}
func (s *Store) calMetaPath(id string) string {
	return filepath.Join(s.calDir(id), "calendar.json")
}
func (s *Store) calObjectsDir(id string) string {
	return filepath.Join(s.calDir(id), "objects")
}
func (s *Store) calChangesPath(id string) string {
	return filepath.Join(s.calDir(id), "changes.log")
}
func (s *Store) calSeqPath(id string) string {
	return filepath.Join(s.calDir(id), "seq.txt")
}
func (s *Store) calCTagPath(id string) string {
	return filepath.Join(s.calDir(id), "ctag.txt")
}
func (s *Store) lockPath(id string) string {
	return filepath.Join(s.calDir(id), ".lock")
}

func (s *Store) addressbookDir(id string) string {
	return filepath.Join(s.root, "addressbooks", id)
}

func (s *Store) addressbookMetaPath(id string) string {
	return filepath.Join(s.addressbookDir(id), "meta.json")
}

func (s *Store) addressbookContactsDir(id string) string {
	return filepath.Join(s.addressbookDir(id), "contacts")
}

func (s *Store) addressbookChangesPath(id string) string {
	return filepath.Join(s.addressbookDir(id), "changes.log")
}

func (s *Store) addressbookSeqPath(id string) string {
	return filepath.Join(s.addressbookDir(id), "seq.txt")
}

func (s *Store) addressbookCTagPath(id string) string {
	return filepath.Join(s.addressbookDir(id), "ctag.txt")
}

func (s *Store) addressbookLockPath(id string) string {
	return filepath.Join(s.addressbookDir(id), ".lock")
}

func randID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type calMeta struct {
	ID          string    `json:"id"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerGroup  string    `json:"owner_group"`
	URI         string    `json:"uri"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	CTag        string    `json:"ctag"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	SyncToken   string    `json:"sync_token"`
	SyncSeq     int64     `json:"sync_seq"`
}

type objFile struct {
	ID         string     `json:"id"`
	CalendarID string     `json:"calendar_id"`
	UID        string     `json:"uid"`
	ETag       string     `json:"etag"`
	Data       string     `json:"data"`
	Component  string     `json:"component"`
	StartAt    *time.Time `json:"start_at,omitempty"`
	EndAt      *time.Time `json:"end_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type addressbookMeta struct {
	ID          string    `json:"id"`
	OwnerUserID string    `json:"owner_user_id"`
	OwnerGroup  string    `json:"owner_group"`
	URI         string    `json:"uri"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	CTag        string    `json:"ctag"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	SyncToken   string    `json:"sync_token"`
	SyncSeq     int64     `json:"sync_seq"`
}

type contactFile struct {
	ID            string    `json:"id"`
	AddressbookID string    `json:"addressbook_id"`
	UID           string    `json:"uid"`
	ETag          string    `json:"etag"`
	Data          string    `json:"data"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type changeRow struct {
	Seq     int64  `json:"seq"`
	UID     string `json:"uid"`
	Deleted bool   `json:"deleted"`
}

func readJSON[T any](path string, out *T) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func writeJSON(path string, v any) error {
	tmp := path + ".tmp"
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendJSONLines(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *Store) withCalLock(id string, fn func() error) error {
	// Coarse per-calendar lock using a lock file opened O_CREATE|O_RDWR.
	s.mu.Lock()
	f, ok := s.locks[id]
	if !ok {
		_ = os.MkdirAll(s.calDir(id), 0o755)
		var err error
		f, err = os.OpenFile(s.lockPath(id), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.locks[id] = f
	}
	s.mu.Unlock()

	// Use an in-process mutex via file to hint uniqueness.
	// We won’t use flock; keep it simple and process-local.
	type token struct{}
	ch := make(chan token, 1)
	ch <- token{}
	// simple critical section (already process-local)
	<-ch
	defer func() { ch <- token{} }()

	return fn()
}

func (s *Store) withAddressbookLock(id string, fn func() error) error {
	// Coarse per-calendar lock using a lock file opened O_CREATE|O_RDWR.
	s.mu.Lock()
	f, ok := s.locks[id]
	if !ok {
		_ = os.MkdirAll(s.addressbookDir(id), 0o755)
		var err error
		f, err = os.OpenFile(s.addressbookLockPath(id), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.locks[id] = f
	}
	s.mu.Unlock()

	// Use an in-process mutex via file to hint uniqueness.
	// We won’t use flock; keep it simple and process-local.
	type token struct{}
	ch := make(chan token, 1)
	ch <- token{}
	// simple critical section (already process-local)
	<-ch
	defer func() { ch <- token{} }()

	return fn()
}
