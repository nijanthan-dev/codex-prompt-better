// Package postgres implements Prompt Better's PostgreSQL persistence boundary.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nijanthan-dev/codex-prompt-better/migrations"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

const migrationTable = "schema_migrations"

// Runner applies immutable embedded migrations using one transaction per file.
type Runner struct {
	provider *goose.Provider
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
	return &Runner{provider: provider}, nil
}

// Up applies all pending migrations.
func (r *Runner) Up(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := r.provider.Up(ctx); err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}
	return nil
}

// DownTo rolls back to a prior version for pre-release validation only. Shipped
// databases use forward repair as documented in the recovery runbook.
func (r *Runner) DownTo(ctx context.Context, version int64) error {
	if version < 0 {
		return errors.New("invalid migration target")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := r.provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("roll back database migrations: %w", err)
	}
	return nil
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

// MigrationFS returns a read-only view used by deterministic migration audits.
func MigrationFS() (fs.FS, error) {
	return migrations.Files, nil
}
