package tiki

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
)

const schemaVersion = 4

// schema.sql is the immutable version 2 baseline. Add numbered migrations and
// increase schemaVersion for future changes; never edit an applied migration.
//
//go:embed schema.sql migrations/*.sql
var schemaFiles embed.FS

// The caller holds one immediate transaction for the entire upgrade, including
// user_version. A failed step rolls back all steps and concurrent opens serialize.
func migrate(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 0 || version == 1 || version > schemaVersion {
		return fmt.Errorf("unsupported database schema %d; this build supports versions 2 through %d (refusing to downgrade or reset data)", version, schemaVersion)
	}
	if version == 0 {
		baseline, err := schemaFiles.ReadFile("schema.sql")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(baseline)); err != nil {
			return fmt.Errorf("create schema 2: %w", err)
		}
		version = 2
	}
	for next := version + 1; next <= schemaVersion; next++ {
		migration, err := schemaFiles.ReadFile(fmt.Sprintf("migrations/%03d.sql", next))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(migration)); err != nil {
			return fmt.Errorf("migrate schema %d to %d: %w", next-1, next, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", next)); err != nil {
			return err
		}
	}
	return nil
}
