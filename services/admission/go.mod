module flash-sale/services/admission

go 1.27.3

// Phase 1 pins; runtime integrations are implemented in later phases.
require (
	github.com/IBM/sarama v1.46.3
	github.com/jackc/pgx/v5 v5.11.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/prometheus/client_golang v1.25.0
)

require flash-sale/internal/platform v0.0.0

replace flash-sale/internal/platform => ../../internal/platform
