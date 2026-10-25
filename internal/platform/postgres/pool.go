// Package postgres restricts each connection pool to one owned database/role.
package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

var owners = map[string][2]string{"admission": {"admission", "flashkart_admission"}, "flash-sale": {"flash_sale", "flashkart_flash_sale"}, "order": {"order_service", "flashkart_order"}}

func Open(ctx context.Context, owner, url string) (*pgxpool.Pool, error) {
	expected, ok := owners[owner]
	if !ok {
		return nil, fmt.Errorf("unknown SQL owner")
	}
	cfg, e := pgxpool.ParseConfig(url)
	if e != nil {
		return nil, fmt.Errorf("invalid %s database configuration", owner)
	}
	if cfg.ConnConfig.User != expected[0] || cfg.ConnConfig.Database != expected[1] {
		return nil, fmt.Errorf("database credentials do not match %s owner", owner)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["application_name"] = owner
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, fmt.Errorf("open %s database", owner)
	}
	var user, db string
	e = p.QueryRow(ctx, "SELECT current_user,current_database()").Scan(&user, &db)
	if e != nil || user != expected[0] || db != expected[1] {
		p.Close()
		return nil, fmt.Errorf("%s database identity unavailable", owner)
	}
	return p, nil
}
