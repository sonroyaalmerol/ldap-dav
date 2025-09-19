package filestore

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	root   string
	mu     sync.Mutex // protects calendar-level lock map
	locks  map[string]*os.File
	logger func(msg string, kv ...any) // optional
}

// New creates or opens a filesystem store rooted at rootDir.
// It will create the directory structure if missing.
func New(rootDir string, logger func(string, ...any)) (*Store, error) {
	if rootDir == "" {
		return nil, errors.New("rootDir required")
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "calendars"), 0o755); err != nil {
		return nil, err
	}
	return &Store{
		root:   rootDir,
		locks:  make(map[string]*os.File),
		logger: logger,
	}, nil
}

func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, f := range s.locks {
		_ = f.Close()
		delete(s.locks, id)
	}
}
