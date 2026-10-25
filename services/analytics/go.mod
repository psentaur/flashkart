module flash-sale/services/analytics

go 1.27.3

// Phase 1 pins; runtime integrations are implemented in later phases.
require (
	github.com/lovoo/goka v1.1.17
	github.com/IBM/sarama v1.46.3
	github.com/prometheus/client_golang v1.25.0
)

require flash-sale/internal/platform v0.0.0

replace flash-sale/internal/platform => ../../internal/platform
