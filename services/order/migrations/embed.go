// Package migrations embeds only Order's owner schema.
package migrations

import _ "embed"

//go:embed 001_orders.sql
var Orders string
