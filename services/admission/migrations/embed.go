// Package migrations embeds the owner's checksummed domain schema.
package migrations

import _ "embed"

//go:embed 001_sales.sql
var Sales string
