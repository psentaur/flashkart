module flash-sale/tests

go 1.27.3

// Phase 1 pins; runtime integrations are implemented in later phases.
require (
	github.com/testcontainers/testcontainers-go v0.44.0
	github.com/testcontainers/testcontainers-go/modules/kafka v0.44.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0
	github.com/lovoo/goka v1.1.17
	github.com/IBM/sarama v1.46.3
)
