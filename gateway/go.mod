module flash-sale/gateway

go 1.27.3

// Phase 1 pins; runtime integrations are implemented in later phases.
require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/prometheus/client_golang v1.25.0
)

require (
	flash-sale/internal/platform v0.0.0
	flash-sale/frontend v0.0.0
)

replace flash-sale/internal/platform => ../internal/platform
replace flash-sale/frontend => ../frontend
