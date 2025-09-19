package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) NewCTag(ctx context.Context, calendarID string) (string, error) {
	var newCTag string
	err := s.withCalLock(calendarID, func() error {
		metaPath := s.calMetaPath(calendarID)
		var meta calMeta
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

func (s *Store) GetSyncInfo(ctx context.Context, calendarID string) (string, int64, error) {
	var meta calMeta
	if err := readJSON(s.calMetaPath(calendarID), &meta); err != nil {
		return "", 0, err
	}
	return meta.SyncToken, meta.SyncSeq, nil
}

func (s *Store) ListChangesSince(ctx context.Context, calendarID string, sinceSeq int64, limit int) ([]storage.Change, int64, error) {
	f, err := os.Open(s.calChangesPath(calendarID))
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

func (s *Store) RecordChange(ctx context.Context, calendarID, uid string, deleted bool) (string, int64, error) {
	var token string
	var seq int64
	err := s.withCalLock(calendarID, func() error {
		metaPath := s.calMetaPath(calendarID)
		var meta calMeta
		if err := readJSON(metaPath, &meta); err != nil {
			return err
		}
		tok, newSeq, err := s.recordChangeLocked(&meta, calendarID, uid, deleted)
		if err != nil {
			return err
		}
		token = tok
		seq = newSeq
		return writeJSON(metaPath, &meta)
	})
	return token, seq, err
}

// recordChangeLocked increments seq, sets sync_token, appends change row.
// Caller must hold calendar lock and pass the loaded meta; meta is updated in-place.
func (s *Store) recordChangeLocked(meta *calMeta, calendarID, uid string, deleted bool) (string, int64, error) {
	// load seq
	seq := meta.SyncSeq
	seq++
	meta.SyncSeq = seq
	meta.SyncToken = "seq:" + strconv.FormatInt(seq, 10)

	// persist seq.txt (optional, for visibility)
	if err := os.WriteFile(s.calSeqPath(calendarID), []byte(strconv.FormatInt(seq, 10)), 0o644); err != nil {
		return "", 0, err
	}
	if err := os.WriteFile(s.calCTagPath(calendarID), []byte(meta.CTag), 0o644); err != nil {
		// best-effort; not critical
		_ = err
	}

	// append change
	row := changeRow{Seq: seq, UID: uid, Deleted: deleted}
	if err := appendJSONLines(s.calChangesPath(calendarID), row); err != nil {
		return "", 0, err
	}
	return meta.SyncToken, seq, nil
}
