# Mukthish implementation boundaries

Phase 1 creates only Mukthish's nine Go modules and local PostgreSQL/Kafka dependencies.
Inventory/Payment, Catalog/Notification, leaderless replicas, and shared cloud infrastructure
remain owner-supplied interfaces. No teammate source or runtime is provisioned here.

- Member 2 supplies cap-aware Inventory RPCs, stable payment/refund events, stock/allowance
  verification and clock/cleanup/restart controls; also platform identities/networking, GitOps,
  monitoring and log access.
- Member 3 supplies Catalog REST/readiness/quorum/fault adapters and authenticated Notification
  query/event invocation adapters.
- Mukthish supplies Admission, Flash Sale, Order, Analytics, frontend/gateway, broker configuration,
  application health/readiness and HPA/load/streaming evidence.

[Draft adoption status](../contracts/adoption.json) remains pending. Bindings are generated from
that draft for independent scaffold validation; affected producers/consumers must agree before
coupled service implementation or integration. Phase 1 does not implement purchase behavior.

The preceding analysis still calls for durable attempt recovery, replay precedence, exact digest
time encoding and attempt aggregate identity. Those belong to the design/story work before their
implementation; setup does not silently resolve them or claim peer approval.
