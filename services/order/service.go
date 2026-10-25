// Package order exports the owned service boundary for runtime and test fixtures.
package order

import (
	"flash-sale/internal/platform/auth"
	orderhttp "flash-sale/services/order/internal/http"
	"flash-sale/services/order/internal/inventory"
	"flash-sale/services/order/internal/order"
	"net/http"
)

type Store = order.Store
type Purchase = order.Purchase
type Order = order.Order
type Offer = order.Offer
type Intent = order.Intent
type Inventory = order.Inventory

var Migrate = order.Migrate
var PayloadHash = order.PayloadHash
var NewInventoryClient = inventory.New
var OfferClient = order.OfferClient

func Handler(s Store, v auth.Verifier) http.Handler { return orderhttp.Handler(s, v) }
