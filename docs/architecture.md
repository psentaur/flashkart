# Flashkart architecture and team boundaries

Flashkart is an eight-service flash-sale demonstration. This document records the agreed
architecture in a shared form; the local planning conversation is intentionally not published.
The [specification](../specs/001-flash-sale-platform/spec.md) defines behavior and acceptance,
the [data model](../specs/001-flash-sale-platform/data-model.md) defines logical ownership, and
[contracts](../specs/001-flash-sale-platform/contracts/README.md) define service boundaries.

## Responsibilities

| Owner | Services | Supporting responsibilities |
| --- | --- | --- |
| Mukthish | Admission, Flash Sale Manager, Order, Sales Analytics | Frontend, gateway, Message Queue; scaling/load evidence (c), stream processing (e) |
| Member 2 | Inventory Reservation, Payment | Shared platform and infrastructure-as-code (a), GitOps (d), observability (h) |
| Member 3 | Product Catalog, Notification | Serverless/communication integration (b), leaderless concept (f), storage products (g) |

Mukthish's components use Go, Goka and Sarama. Goka replaces the earlier
analytics-engine choice; Sarama is a Kafka client, while Kafka remains the Message Queue broker.
The other members choose their own languages, frameworks, database engines and deployment tools.
These are coordination leads; producers and consumers review their shared contracts together.

## Purchase and state ownership

1. Flash Sale publishes a versioned configuration. Catalog, Admission and Inventory acknowledge
   matching readiness; activation is durable and allowed only within the configured sale window.
2. The gateway routes browsing and purchase requests. Admission enforces shared per-shopper,
   per-sale and in-flight limits across replicas; admission approval is not a stock guarantee.
3. Order records a stable request identity and asks Inventory to reserve through gRPC. Inventory
   atomically holds stock; only after a durable hold does Order accept a pending order.
4. Payment runs asynchronously through Kafka. Order confirms Inventory before reporting success,
   or releases/expires the order and retains a refund obligation for an unfulfillable paid hold.
5. Notification and Analytics consume independently. They do not hold up checkout or settlement.

REST handles the public gateway and Admission-to-Order link; gRPC handles Order-to-Inventory;
Kafka handles commands, outcomes, configuration and analytics input/results. Every service owns
its persistent business state; consumers do not write another service's database. Durable inbox/
outbox records and stable operation IDs tolerate retries, duplicates and interrupted processing.
Inventory alone resolves reservation confirmation, release and expiry. The stock invariant is
available + held + confirmed sold = allocated, with all quantities nonnegative.

## Analytics and leaderless metadata

Analytics uses Goka on the managed Kubernetes platform, with raw input, compacted group state and
separate result topics. Logical event deduplication and revisioned projections preserve counts on
replay. Event occurrence time selects a one-minute window; recorded broker arrival and ordered
clock markers enforce the 60-second lateness allowance. A durable-state View/relay makes results
recoverable without assuming transactional exactly-once delivery.

Catalog metadata uses three independently persisted fixed-member replicas, no permanent leader,
read/write quorums of two, causal version contexts, preserved concurrent siblings and explicit
conflict resolution. A returning node must repair and converge. This store is not authoritative
for stock, sale prices, orders or payment. Its implementation stack belongs to Member 3.

## Shared deployment and demonstration requirements

The original six ordinary services plus Analytics run on managed Kubernetes; Notification is
event-triggered serverless. The platform owner selects provider and tooling. Provision resources
through versioned infrastructure definitions; reconcile Kubernetes releases through GitOps.
Admission and Order demonstrate actual resource-based HPA scale-out under sustained purchase load.
Use Prometheus/Grafana and centralized logs spanning all eight services, public HTTPS for approved
routes, and private Inventory/Payment operations. Demonstrate distinct transactional, object and
replica block-storage products with meaningful application uses.

The requirement categories (a)-(h) are retained from the recorded project requirements. The
original assignment attachment is not in this repository. The agreed Goka amendment and the
three-person ownership split take precedence over earlier technology or four-person suggestions.

## Branch and review workflow

`main` contains shared governance, behavior, logical data/interface contracts and Spec Kit tools.
`001-lead-implementation` adds Mukthish's technical plan, research, validation guide and
100-task backlog. Members create their own implementation branches and select their stacks there.
Shared/breaking contract changes require affected producer/consumer review before merge. Task
completion on a personal branch does not certify another member's implementation or live evidence.
