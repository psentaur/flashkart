package service

import (
	"context"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
)

func workflowMetrics(db *pgxpool.Pool, service string, out io.Writer, ctx context.Context) {
	var query string
	switch service {
	case "admission-controller":
		query = "SELECT count(*) FROM admission.purchase_outbox WHERE sent_at IS NULL"
	case "inventory-reservation":
		query = "SELECT count(*) FROM inventory.order_work WHERE delivered_at IS NULL"
	case "order-service":
		query = "SELECT count(*) FROM orders.purchases WHERE state='PENDING'"
	case "flash-sale-manager":
		query = "SELECT count(*) FROM flash_sale.drafts WHERE state='PENDING'"
	}
	if query != "" {
		var pending int64
		if db.QueryRow(ctx, query).Scan(&pending) == nil {
			fmt.Fprintf(out, "# TYPE flashkart_workflow_pending gauge\nflashkart_workflow_pending{service=%q} %d\n", service, pending)
		}
	}
	if service != "inventory-reservation" {
		return
	}
	rows, err := db.Query(ctx, "SELECT sale_id,available_stock FROM inventory.sales WHERE active")
	if err != nil {
		return
	}
	defer rows.Close()
	fmt.Fprintln(out, "# TYPE flashkart_inventory_available_stock gauge")
	for rows.Next() {
		var sale string
		var stock int64
		if rows.Scan(&sale, &stock) == nil {
			fmt.Fprintf(out, "flashkart_inventory_available_stock{sale_id=%q} %d\n", sale, stock)
		}
	}
}
