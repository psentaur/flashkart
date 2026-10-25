// Package gateway provides the HTTP/template boundary without owning domain state.
package gateway

import (
	"flash-sale/gateway/internal/routes"
	"net/http"
)

type Config = routes.Config

func Handler(c Config) http.Handler { return routes.Handler(c) }
