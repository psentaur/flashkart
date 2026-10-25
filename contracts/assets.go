package contracts

import "embed"

// Assets are the same reviewed wire contracts used by generation and CI.
//
//go:embed *.json
var Assets embed.FS
