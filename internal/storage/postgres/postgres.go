package postgres

import (
	"context"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

type Store struct {
	pool   *pgxpool.Pool
	logger zerolog.Logger
}

func New(dsn string, logger zerolog.Logger) (*Store, error) {
	if err := runMigrations(dsn, logger); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool, logger: logger}, nil
}

func runMigrations(dsn string, logger zerolog.Logger) error {
	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create source driver: %w", err)
	}

	m, err := migrate.NewWithSourceInstance(
		"iofs",
		sourceDriver,
		dsn,
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

func (s *Store) Close() { s.pool.Close() }
