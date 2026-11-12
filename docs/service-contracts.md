# Service contracts and ownership

The REST contract lives in [OpenAPI](../contracts/openapi/flashkart.v1.json). Internal services use [gRPC](../contracts/proto/flashkart/v1/services.proto). The [event schemas](../contracts/events) define messages independently of the transport implementation.

| State | Owner | Allowed transitions |
| --- | --- | --- |
| Publication | Flash Sale Manager | DRAFT → PENDING → PUBLISHED. PENDING resumes preparation, replication and activation with the original operation identity. Terms freeze at PENDING. |
| Sale execution | Inventory | Missing → prepared → active. Replays require identical terms. |
| Reservation | Inventory | Missing → HELD → COMMITTED, RELEASED or EXPIRED. Terminal states cannot change. Rejection stores an attempt outcome without creating a reservation. |
| Order | Order | Missing → PENDING → CONFIRMED or CANCELLED. Confirmation requires the committed reservation. |
| Payment | Payment | Missing → PENDING → SUCCEEDED or DECLINED. SUCCEEDED → REFUNDED. Transport failure leaves the outcome unknown to the caller. |
| Purchase attempt | Admission and terminal projection | QUEUED → REJECTED, CONFIRMED or CANCELLED. Admission owns durable acceptance. Inventory owns reservation rejections and Order owns outcomes after reservation. |

Admission produces attempt acceptance and throttling events. Inventory produces reservation and stock-rejection events. Order produces confirmed and cancelled events. Payment produces payment lifecycle events. Admission, Inventory and Order may emit terminal outcome messages for the work they own. Notification stores customer-scoped outcomes. The analytics companion produces partition heartbeats, and Flink produces window snapshots.

Use UTC RFC 3339 timestamps, stable opaque IDs, and integer minor currency units. The attempt ID and HTTP Idempotency-Key header must match. Retries reuse original identifiers and payloads. An event ID survives outbox replay. HTTP correlation IDs are propagated into gRPC call metadata and the event trace field. Counter intervals in HTTP are unsigned decimal strings to preserve uint64 precision in browsers. Intervals are inclusive, sorted and non-overlapping.

A replicated write with incomplete acknowledgements uses WRITE_OUTCOME_UNKNOWN. The caller can retry a prepared envelope or read state. Catalog conflict responses include siblings and the causal context used for resolution. Internal services use gRPC status codes, with a stable domain reason for errors that share a transport code. Quorum loss and unknown write outcome both map to UNAVAILABLE with distinct reasons. Phase 1 shell readiness reports process readiness.

Demo authentication uses separate bearer tokens for customer and administrator identities. Future business handlers resolve the customer from the verified token and scope attempts, orders and notifications to that identity. Product and published sale reads are public. Drafts, mutations, conflict resolution and replica inspection require administrator authorization. Secrets are supplied at runtime. The local profile uses dedicated emulator credentials and explicit endpoint overrides when enabled. The AWS profile uses workload roles and standard service endpoints. Each replica receives only its own DSN secret, and application services receive their own transactional database role.
