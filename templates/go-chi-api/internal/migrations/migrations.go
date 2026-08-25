package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
)

//go:embed sql/*.sql
var files embed.FS

func Run(ctx context.Context, connection *pgx.Conn) error {
	if _, err := connection.Exec(ctx, `select pg_advisory_lock(734621980)`); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), `select pg_advisory_unlock(734621980)`) }()
	if _, err := connection.Exec(ctx, `create table if not exists schema_migrations (version text primary key, checksum text not null, applied_at timestamptz not null default now())`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := fs.Glob(files, "sql/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)
	for _, path := range entries {
		migration, err := files.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}
		checksum := migrationChecksum(migration)
		var appliedChecksum string
		err = connection.QueryRow(ctx, `select checksum from schema_migrations where version=$1`, path).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum != checksum {
				return fmt.Errorf("migration %s changed after it was applied", path)
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check migration %s: %w", path, err)
		}
		transaction, err := connection.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", path, err)
		}
		if _, err := transaction.Exec(ctx, string(migration)); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("execute migration %s: %w", path, err)
		}
		if _, err := transaction.Exec(ctx, `insert into schema_migrations(version, checksum) values($1, $2)`, path, checksum); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", path, err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", path, err)
		}
	}
	return nil
}

func migrationChecksum(migration []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(migration))
}
