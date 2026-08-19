package store

import (
	"context"
	"database/sql"
	"fmt"
)

// addedColumns lists every column added to an existing table AFTER that
// table's original CREATE statement shipped.
//
// schema.sql alone is not enough for these. CREATE TABLE IF NOT EXISTS is a
// no-op against a database file that already has the table, so a column added
// to the CREATE statement reaches new files only — an existing one keeps the
// old shape and every query naming the new column fails. SQLite has no
// ADD COLUMN IF NOT EXISTS, so the presence check is done explicitly against
// PRAGMA table_info.
//
// The alternative — dropping and recreating the table — would silently
// discard an operator's live ephemeral mocks on upgrade. They are disposable
// by design (constitution Principle III), but disposable means "safe to
// lose", not "thrown away without being asked".
//
// Each entry's DDL must stay valid as an ALTER: SQLite requires an added
// column to be nullable or to carry a non-NULL default, since it has to
// materialise a value for every existing row.
var addedColumns = []struct {
	table  string
	column string
	ddl    string
}{
	// 003 byte-stream data plane: the per-rule projection override and the
	// captured-traffic provenance marker (data-model.md §9).
	{table: "ephemeral_mocks", column: "projection_blob", ddl: "ALTER TABLE ephemeral_mocks ADD COLUMN projection_blob BLOB"},
	{table: "ephemeral_mocks", column: "from_capture", ddl: "ALTER TABLE ephemeral_mocks ADD COLUMN from_capture INTEGER NOT NULL DEFAULT 0"},
}

// applyAddedColumns brings an existing database file up to the current
// schema. It runs after schema.sql (which creates anything missing outright)
// and is idempotent: a column already present is skipped, so repeated boots
// against the same file are free.
func applyAddedColumns(ctx context.Context, db *sql.DB) error {
	for _, c := range addedColumns {
		has, err := hasColumn(ctx, db, c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := db.ExecContext(ctx, c.ddl); err != nil {
			return fmt.Errorf("add column %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// hasColumn reports whether table already has column. A table that does not
// exist at all yields no rows and therefore false, which is correct: the
// caller has already run schema.sql, so a still-absent table means this
// database does not have that table by design.
func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	// PRAGMA table_info takes no bind parameters in SQLite, so the table
	// name is interpolated. Every value reaching here is a compile-time
	// constant from addedColumns above — never caller input — so there is
	// no injection surface.
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			cid        int
			name       string
			declType   sql.NullString
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &declType, &notNull, &dfltValue, &primaryKey); err != nil {
			return false, fmt.Errorf("inspect %s: scan: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	return false, nil
}
