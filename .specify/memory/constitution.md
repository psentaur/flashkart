# Flash Sale Constitution

## Core Principles

### I. Readable, Consistent Code

Code MUST use descriptive names, explicit control flow, and modules with a single cohesive
responsibility. Each introduced language MUST have documented formatting, linting, and applicable
static analysis commands in its feature plan and development instructions. Changes MUST pass
those checks before merge. New abstractions and dependencies MUST solve a documented present
need; their rationale and maintenance cost MUST be recorded in the plan or pull request.
Comments MUST explain non-obvious constraints and decisions rather than repeat the code.
These rules keep a three-person team able to review and maintain each other's services.

### II. Explicit Boundaries and Contracts

Each service MUST own its business rules and persistent state. Other services MUST access that
state through documented APIs or events rather than write directly to its database. API and event
changes MUST update versioned contracts, including examples, validation rules, error semantics,
retry behavior, and idempotency expectations. Producers and affected consumers MUST review
contract changes before merge. Breaking changes MUST include a migration and compatibility plan.
Money MUST use integer minor units with an explicit currency; exchanged timestamps MUST identify
UTC. Explicit contracts allow independent implementation without hidden coupling.

### III. Behavior-Based Verification

Every feature MUST define measurable acceptance criteria and map them to automated checks at the
lowest effective level: unit tests for business rules, integration tests for persistence and
service boundaries, contract tests for interfaces, and end-to-end tests for critical workflows.
New or changed business behavior MUST include tests for successful and rejected outcomes.
Bug fixes MUST include a regression test that demonstrates the defect before the fix, unless
reproduction cannot be automated; that exception MUST record the reason and repeatable manual
verification. Tests MUST control time, randomness, and dependencies where needed for repeatable
results. Arbitrary delays and retries MUST NOT be used to hide failures. Coverage percentages
MUST NOT substitute for verifying acceptance criteria. Documentation-only changes MUST validate
paths, commands, and consistency across affected artifacts.

### IV. Distributed Correctness Under Failure

Purchase features MUST document state transitions, invariants, and the service responsible for
each transition. Automated concurrency and failure tests MUST verify that reservations cannot
exceed available stock, repeated requests and duplicate events do not repeat business effects,
reservation expiry races with payment are resolved deterministically, and late successful
payments receive the specified compensation. State changes requiring event publication MUST use
a transactional outbox or a documented equivalent that survives process failure without losing
the event. Consumers MUST tolerate redelivery and specify handling for out-of-order events.

Authoritative inventory, order, and payment state MUST retain transactional guarantees; catalog
quorum replication MUST NOT be treated as an atomic stock reservation mechanism. Leaderless
storage changes MUST test quorum loss, concurrent-version conflicts, durable restart, and recovery
of stale replicas. Recovery tests MUST verify data convergence under the documented conflict
policy. These checks protect correctness when individual requests or components fail.

### V. Maintainability and Operational Clarity

Every runnable component MUST document its purpose, owner, configuration, startup, test commands,
and dependency requirements. Changes to behavior, contracts, configuration, or recovery procedures
MUST update the corresponding documentation in the same change. Services MUST emit structured
logs with correlation identifiers across requests and events, expose health information, and
provide metrics sufficient to diagnose errors and latency. Logs MUST exclude secrets and payment
credentials. Operational changes MUST document rollback or recovery steps. Refactoring MUST
preserve verified behavior; removal of obsolete code and dependencies MUST accompany replacement
once compatibility obligations are satisfied. These requirements make handoffs and incident
recovery reproducible.

## Engineering Standards

- Specifications MUST describe behavior and acceptance criteria; plans MUST record technology,
  tooling, data ownership, contracts, and consequential design tradeoffs before implementation.
- Changes MUST preserve the latest agreed architecture decisions or explicitly document and
  obtain review for a proposed deviation. Earlier suggestions in the design discussion MUST NOT
  override later decisions. This constitution does not select a cloud provider or language.
- New components MUST define a test framework, test naming convention, and executable validation
  commands. Until application code exists, no application build or test command is presumed.
- Tests requiring databases, queues, or multiple replicas MUST document a reproducible setup,
  isolation strategy, and cleanup procedure.
- Performance-sensitive changes MUST define a workload and measurable acceptance limits in their
  feature plan and report throughput, latency, error rates, and reservation correctness under that
  workload. Shared admission limits MUST remain valid across replicas.

## Development Workflow and Quality Gates

1. Before implementation, create or update the feature specification, plan, and tasks under
   `specs/###-feature-name/`. Tasks MUST identify owners and cross-service dependencies.
2. Agree on affected contracts and state transitions with producers and consumers before parallel
   implementation. Keep changes small enough for a reviewer to assess behavior and risks.
3. Before merge, run the affected components' formatting, linting, static analysis, and relevant
   automated tests. Cross-service changes MUST run the affected contract and integration checks;
   purchase-flow changes MUST verify the impacted end-to-end acceptance criteria.
4. Each pull request MUST link its specification or issue, describe behavior changes, report
   validation commands and results, and identify compatibility or operational risks. A teammate
   MUST review it; affected contract consumers MUST review interface changes before merge.
5. Required checks MUST pass. A failed check MUST be fixed or covered by an approved, documented,
   time-bounded exception under Governance. Unavailable checks MUST be reported as unverified,
   with an owner and a completion condition; they MUST NOT be reported as passed.
6. Reviewers MUST check applicable constitution rules and consistency among specifications, plans,
   tasks, contracts, implementation, and documentation. CI MUST run the required automated checks
   once runnable components are introduced.

## Governance

This constitution is the governing engineering policy for the repository. Feature plans and local
conventions MUST comply with it. Amendments MUST be proposed in a pull request that records the
reason, affected principles, compatibility impact, and any migration work. At least one other
team member MUST approve an amendment, and owners of affected service contracts MUST review it.
Approval and merge establish the amended policy.

Constitution versions MUST follow semantic versioning: MAJOR for incompatible principle removal
or redefinition, MINOR for new principles or materially expanded obligations, and PATCH for
clarifications without changed obligations. Each amendment MUST update the version and amendment
date while retaining the original ratification date. Version 1.0.0 records initial adoption of
this previously unfilled scaffold on 2026-10-25.

Exceptions MUST identify the specific rule, justification, affected scope, risk mitigation, owner,
and expiry or removal condition in the feature plan or pull request. A teammate and affected
service owners MUST approve an exception before merge. Exceptions MUST NOT silently redefine
policy; repeated exceptions MUST trigger a proposed amendment or corrective work. Compliance MUST
be checked during planning and pull request review, with unresolved violations tracked as tasks.

**Version**: 1.0.0 | **Ratified**: 2026-10-25 | **Last Amended**: 2026-10-25
