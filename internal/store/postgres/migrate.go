// Package postgres implements Prompt Better's PostgreSQL persistence boundary.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nijanthan-dev/codex-prompt-better/migrations"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

const migrationTable = "schema_migrations"

// ErrSchemaResetRequired rejects incompatible pre-release schemas.
var ErrSchemaResetRequired = errors.New("pre-release schema is incompatible; create an encrypted backup, reset the local database, then migrate")

// Runner applies immutable embedded migrations using one transaction per file.
type Runner struct {
	provider *goose.Provider
	db       *sql.DB
}

// OpenMigrationDB opens a PostgreSQL connection for explicit migration work.
func OpenMigrationDB(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, errors.New("migration database reference is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open migration database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// NewRunner constructs a migration runner over an operator-owned connection.
func NewRunner(db *sql.DB) (*Runner, error) {
	if db == nil {
		return nil, errors.New("migration database is required")
	}
	store, err := database.NewStore(database.DialectPostgres, migrationTable)
	if err != nil {
		return nil, fmt.Errorf("configure migration store: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("configure migration lock: %w", err)
	}
	provider, err := goose.NewProvider("", db, migrations.Files,
		goose.WithStore(store), goose.WithDisableGlobalRegistry(true),
		goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("configure migrations: %w", err)
	}
	return &Runner{provider: provider, db: db}, nil
}

// Up applies all pending migrations.
func (r *Runner) Up(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.provider == nil || r.db == nil {
		return errors.New("migration runner is not configured")
	}
	reset, err := r.requiresReset(ctx)
	if err != nil {
		return err
	}
	if reset {
		return ErrSchemaResetRequired
	}
	if _, err := r.provider.Up(ctx); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}

func (r *Runner) requiresReset(ctx context.Context) (bool, error) {
	var migrationsExist bool
	if err := r.db.QueryRowContext(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&migrationsExist); err != nil {
		return false, fmt.Errorf("inspect migration metadata: %w", err)
	}
	if !migrationsExist {
		return false, nil
	}
	var version int64
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(max(applied.version_id),0)
		FROM public.schema_migrations applied
		WHERE applied.is_applied AND NOT EXISTS (
			SELECT 1 FROM public.schema_migrations later
			WHERE later.version_id=applied.version_id AND later.id>applied.id)`).Scan(&version); err != nil {
		return false, fmt.Errorf("inspect schema compatibility: %w", err)
	}
	if version > LatestSchemaVersion {
		return true, nil
	}
	if version < 2 {
		return false, nil
	}
	var baselineTableExists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT to_regclass('prompt_better.execution_events') IS NOT NULL`).Scan(&baselineTableExists); err != nil {
		return false, fmt.Errorf("inspect schema baseline: %w", err)
	}
	return !baselineTableExists, nil
}

// Version returns the latest applied migration version.
func (r *Runner) Version(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	version, err := r.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}
