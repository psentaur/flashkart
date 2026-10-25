// Package flashsale exports the service boundary for runtime and integration fixtures.
package flashsale

import (
	"flash-sale/internal/platform/auth"
	salehttp "flash-sale/services/flash-sale/internal/http"
	"flash-sale/services/flash-sale/internal/sale"
	"net/http"
)

type Store = sale.Store
type Write = sale.Write
type Sale = sale.Sale
type Limits = sale.Limits
type Offer = sale.Offer

var Migrate = sale.Migrate
var Digest = sale.Digest
var Code = sale.Code

func Handler(s Store, v auth.Verifier) http.Handler { return salehttp.Handler(s, v) }
