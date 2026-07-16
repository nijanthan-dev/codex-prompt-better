package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/nijanthan-dev/codex-prompt-better/migrations"
)

// Execer is the minimum context-aware operation needed for role bootstrap.
type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// BootstrapRoles applies the reviewed role bootstrap file using an administrator
// connection. It is never called from runtime startup.
func BootstrapRoles(ctx context.Context, db Execer) error {
	if db == nil {
		return errors.New("administrator database is required")
	}
	script, err := fs.ReadFile(migrations.AdminFiles, "admin/roles.sql")
	if err != nil {
		return fmt.Errorf("read embedded role bootstrap: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(script)); err != nil {
		return fmt.Errorf("apply role bootstrap: %w", err)
	}
	return nil
}
