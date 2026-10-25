module flash-sale/internal/platform

go 1.27.3

// Phase 1 pins; runtime integrations are implemented in later phases.
require (
	github.com/jackc/pgx/v5 v5.11.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/prometheus/client_golang v1.25.0
	github.com/IBM/sarama v1.46.3
)
