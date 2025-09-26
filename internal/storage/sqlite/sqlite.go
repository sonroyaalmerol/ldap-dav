package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/rs/zerolog"
	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	logger zerolog.Logger
}

func New(dsn string, logger zerolog.Logger) (*Store, error) {
	dir := filepath.Dir(dsn)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Enable foreign keys and WAL mode for better performance
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	if err := runMigrations(db, logger); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &Store{db: db, logger: logger}, nil
}

func runMigrations(db *sql.DB, logger zerolog.Logger) error {
	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create source driver: %w", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create database driver: %w", err)
	}

	m, err := migrate.NewWithInstance(
		"iofs",
		sourceDriver,
		"sqlite",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	if dirty {
		logger.Warn().
			Uint("version", version).
			Msg("Database is in dirty state, forcing version")
		if err := m.Force(int(version)); err != nil {
			return fmt.Errorf("failed to force migration version: %w", err)
		}
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	if err == migrate.ErrNoChange {
		logger.Info().Msg("No new migrations to apply")
	} else {
		newVersion, _, _ := m.Version()
		logger.Info().
			Uint("from_version", version).
			Uint("to_version", newVersion).
			Msg("Migrations applied successfully")
	}

	return nil
}

func (s *Store) Close() {
	_ = s.db.Close()
}
