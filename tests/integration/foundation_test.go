//go:build integration

package integration

import (
	"context"
	"flash-sale/internal/platform/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("ORDER_DATABASE_URL")
	if url == "" {
		t.Fatal("ORDER_DATABASE_URL required; use make test-integration")
	}
	p, e := postgres.Open(context.Background(), "order", url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	return p
}
func TestMigrationRollbackAndChecksum(t *testing.T) {
	ctx := context.Background()
	p := database(t)
	name := "foundation_probe"
	_, _ = p.Exec(ctx, "DROP TABLE IF EXISTS foundation_probe")
	bad := []postgres.Migration{{Name: name, SQL: "CREATE TABLE foundation_probe (id int); SELECT 1/0;"}}
	if postgres.Migrate(ctx, p, bad) == nil {
		t.Fatal("failed migration passed")
	}
	var absent bool
	_ = p.QueryRow(ctx, "SELECT to_regclass('foundation_probe') IS NULL").Scan(&absent)
	if !absent {
		t.Fatal("DDL survived rollback")
	}
	good := []postgres.Migration{{Name: name, SQL: "CREATE TABLE foundation_probe (id int)"}}
	if e := postgres.Migrate(ctx, p, good); e != nil {
		t.Fatal(e)
	}
	if e := postgres.Migrate(ctx, p, good); e != nil {
		t.Fatal(e)
	}
	good[0].SQL += "; -- drift"
	if postgres.Migrate(ctx, p, good) == nil {
		t.Fatal("checksum drift accepted")
	}
	_, _ = p.Exec(ctx, "DROP TABLE foundation_probe; DELETE FROM platform_migrations WHERE name='foundation_probe'")
}
