package filestore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

// Paths
func (s *Store) schedBaseDir() string {
	return filepath.Join(s.root, "scheduling")
}
func (s *Store) schedInboxDir(userID string) string {
	return filepath.Join(s.schedBaseDir(), userID, "inbox")
}
func (s *Store) schedMsgPath(userID, uid string) string {
	return filepath.Join(s.schedInboxDir(userID), uid+".json")
}
func (s *Store) userBaseDir() string {
	return filepath.Join(s.root, "users")
}
func (s *Store) userSettingsPath(userID string) string {
	return filepath.Join(s.userBaseDir(), userID, "scheduling.json")
}

// On-disk schemas
type schedMsgFile struct {
	UID        string    `json:"uid"`
	Method     string    `json:"method"`
	Data       string    `json:"data"`        // raw ICS content
	ReceivedAt time.Time `json:"received_at"` // when stored
	Processed  bool      `json:"processed"`
}

type userSchedulingSettings struct {
	DefaultCalendarID string    `json:"default_calendar_id"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (s *Store) ProcessSchedulingMessage(ctx context.Context, recipient string, icsData []byte, method string) error {
	// extract UID from ICS; fall back if not found
	uid := extractUIDFromICS(string(icsData))
	if uid == "" {
		uid = randID()
	}
	dir := s.schedInboxDir(recipient)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	msg := schedMsgFile{
		UID:        uid,
		Method:     method,
		Data:       string(icsData),
		ReceivedAt: time.Now().UTC(),
		Processed:  false,
	}
	return writeJSON(s.schedMsgPath(recipient, uid), &msg)
}

func (s *Store) GetSchedulingInboxObjects(ctx context.Context, userID string) ([]*storage.SchedulingMessage, error) {
	dir := s.schedInboxDir(userID)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*storage.SchedulingMessage
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		var mf schedMsgFile
		if err := readJSON(filepath.Join(dir, ent.Name()), &mf); err != nil {
			continue
		}
		out = append(out, &storage.SchedulingMessage{
			ID:         mf.UID, // use UID as ID
			UserID:     userID,
			UID:        mf.UID,
			Method:     mf.Method,
			Data:       mf.Data,
			ReceivedAt: mf.ReceivedAt,
			Processed:  mf.Processed,
		})
	}
	return out, nil
}

func (s *Store) DeleteSchedulingInboxObject(ctx context.Context, userID, uid string) error {
	if uid == "" {
		return errors.New("uid required")
	}
	return os.Remove(s.schedMsgPath(userID, uid))
}

func (s *Store) GetScheduleTag(ctx context.Context, calendarID, uid string) (string, error) {
	var of objFile
	if err := readJSON(s.objPath(calendarID, uid), &of); err != nil {
		return "", err
	}
	return of.ScheduleTag, nil
}

func (s *Store) UpdateScheduleTag(ctx context.Context, calendarID, uid string) (string, error) {
	return s.updateObjectFile(calendarID, uid, func(of *objFile) error {
		of.ScheduleTag = randID()
		of.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) updateObjectFile(calendarID, uid string, mutate func(*objFile) error) (string, error) {
	path := s.objPath(calendarID, uid)
	var of objFile
	if err := readJSON(path, &of); err != nil {
		return "", err
	}
	if err := mutate(&of); err != nil {
		return "", err
	}
	if err := writeJSON(path, &of); err != nil {
		return "", err
	}
	return of.ScheduleTag, nil
}

func (s *Store) GetDefaultCalendar(ctx context.Context, userID string) (string, error) {
	path := s.userSettingsPath(userID)
	var set userSchedulingSettings
	if err := readJSON(path, &set); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return set.DefaultCalendarID, nil
}

func (s *Store) SetDefaultCalendar(ctx context.Context, userID, calendarID string) error {
	dir := filepath.Dir(s.userSettingsPath(userID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	set := userSchedulingSettings{
		DefaultCalendarID: calendarID,
		UpdatedAt:         time.Now().UTC(),
	}
	return writeJSON(s.userSettingsPath(userID), &set)
}

func (s *Store) GetCalendarTransparency(ctx context.Context, calendarID string) (string, error) {
	var meta calMeta
	if err := readJSON(s.calMetaPath(calendarID), &meta); err != nil {
		return "", err
	}
	if meta.ScheduleTransp == "" {
		return "opaque", nil
	}
	return meta.ScheduleTransp, nil
}

func (s *Store) SetCalendarTransparency(ctx context.Context, calendarID string, transp string) error {
	return s.withCalLock(calendarID, func() error {
		metaPath := s.calMetaPath(calendarID)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}
		meta.ScheduleTransp = strings.ToLower(transp)
		meta.UpdatedAt = time.Now().UTC()
		return writeJSON(metaPath, &meta)
	})
}

func extractUIDFromICS(ics string) string {
	lines := strings.Split(ics, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "UID:") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "UID:"))
		}
	}
	return ""
}
