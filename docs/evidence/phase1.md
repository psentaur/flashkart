# Phase 1 implementation evidence

Date: 2026-10-25. Owner: Mukthish. Scope: T001–T008 on `001-mukthish-implementation`.

## Implemented

Nine Go 1.27.3 modules/workspace and checksums; pinned runtime/test/tool dependencies; the seven
canonical draft.2 contracts and generated Inventory Go bindings; local PostgreSQL/KRaft Kafka
Compose definitions; private random configuration generation; module quality/CI commands;
ownership/peer handoffs; four service startup shells, gateway and embedded frontend placeholder.
The GitHub Actions workflow runs the same quality/startup commands with full-commit-pinned actions;
its commands were validated locally, but no GitHub workflow run is claimed before a push.
No Inventory, Payment, Catalog, Notification, replica store or shared cloud source was created.

## Verified commands

- `make bootstrap`: exact toolchain/dependencies installed; source and artifact checksums pinned.
- `make check`: gofmt, go vet, staticcheck and compilation pass for all nine modules; four Go
  bootstrap behavior tests pass with `-race`; five Python environment/credential-boundary tests pass; all
  module zip checksums and the single Sarama v1.46.3 workspace selection verify.
- `make contracts-check`: three OpenAPI documents, 24 event examples, five HTTP pairs and
  33 rejection controls pass, including cap bounds/presence, default-one input, error-code
  requirements, the cap-aware digest fixture and source/generated-binding drift (the index link is rebased
  during promotion; all wire/fixture files match the source byte-for-byte).
- `make deps-config`: Compose parses with required credential inputs without rendering secrets.
- `make deps-up`, `make deps-check`, `make deps-status`: actual rootless Podman containers became
  healthy. Three service logins work; cross-service database CONNECT is denied; Flash Sale's
  btree_gist extension is present; Kafka's broker API responds. The locked image digests are in
  `dev/tool-versions.json`. A task-private Podman API socket was used, not a cloud deployment.
- `make smoke-phase1`: all five real binaries return liveness 200, unfinished readiness 503 and
  no purchase route; the gateway serves embedded HTML/CSS. All exit zero on SIGTERM.
- Expected negative gates: `make contracts-integration-check` fails pending E4 review;
  `make dev-up` and `make test-e2e` fail with the explicit later-phase message.

## Remaining scope and retained state

T009–T100 remain pending. The readiness response `phase_1_scaffold` intentionally prevents these
shells being treated as completed services. Live Sarama/Goka/database application behavior,
purchase correctness, peer integration, HPA, deployment and cloud demonstrations were not tested.
Producer/consumer draft adoption is pending in `contracts/adoption.json`; setup does not close
the preceding analysis findings or assert peer approval.

The two task-local dependency containers and their network were stopped/removed after validation.
Their two named volumes and ignored mode-0600 environment were retained together for reuse;
no volume reset or generic destructive cleanup ran. The temporary container API stopped as well.
Use the runtime prerequisites in the development guide before restarting dependencies.

Generated tools/caches, `.env` and the original design conversation stay excluded from version
control. Phase 1 is published on `001-mukthish-implementation`; publication does not approve
draft.2 adoption or certify the later-phase business behavior.
