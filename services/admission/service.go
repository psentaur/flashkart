// Package admission exposes the applied sale projection to runtime and fixtures.
package admission

import (
	admissionhttp "flash-sale/services/admission/internal/http"
	"flash-sale/services/admission/internal/quota"
	"flash-sale/services/admission/internal/sale"
)

type Store = sale.Store
type Projection = sale.Projection

var Migrate = sale.Migrate

type QuotaStore = quota.Store
type Limits = quota.Limits
type Decision = quota.Decision
type Config = admissionhttp.Config

var Handler = admissionhttp.Handler
