package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
)

type Migration struct{ Name, SQL string }

// Migrate serializes migration runners and commits DDL with its checksum atomically.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations []Migration) error {
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{})
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(73190412)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS platform_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		return e
	}
	sorted := append([]Migration(nil), migrations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	seen := map[string]bool{}
	for _, m := range sorted {
		if m.Name == "" || seen[m.Name] {
			return fmt.Errorf("empty or duplicate migration")
		}
		seen[m.Name] = true
		sum := sha256.Sum256([]byte(m.SQL))
		checksum := hex.EncodeToString(sum[:])
		var prior string
		e = tx.QueryRow(ctx, "SELECT checksum FROM platform_migrations WHERE name=$1", m.Name).Scan(&prior)
		if e == nil {
			if checksum != prior {
				return fmt.Errorf("migration checksum changed: %s", m.Name)
			}
			continue
		}
		if e != pgx.ErrNoRows {
			return e
		}
		if _, e = tx.Exec(ctx, m.SQL); e != nil {
			return fmt.Errorf("migration %s failed: %w", m.Name, e)
		}
		if _, e = tx.Exec(ctx, "INSERT INTO platform_migrations(name,checksum) VALUES($1,$2)", m.Name, checksum); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
