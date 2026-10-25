// Package admission exposes the applied sale projection to runtime and fixtures.
package admission

import "flash-sale/services/admission/internal/sale"

type Store = sale.Store
type Projection = sale.Projection

var Migrate = sale.Migrate
