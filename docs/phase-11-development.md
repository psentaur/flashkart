# Phase 11 local Kubernetes

Use a kind host with capacity for both HPAs to reach five replicas while Kafka, Flink, five PostgreSQL instances, and the observability stack are running. Allow at least 8 available CPUs and 12 GiB of available memory for the rehearsal. The single-node overlay uses a shared ReadWriteOnce volume for Flink checkpoints. Install kind, kubectl, a supported container engine, Maven with Java 17, AWS CLI, k6, and the signed AWS Emulator binary.

Build once per immutable tag. In the Git branch Argo CD will watch, update the image tags in both `deploy/kubernetes/kind/kustomization.yaml` and `deploy/kubernetes/platform/kustomization.yaml` to the same tag. Publish the manifest commit to a Git repository accessible from the cluster. Use a new tag and load its images before publishing a later rollout commit.

```sh
export IMAGE_TAG=phase11-demo-1
export AWS_EMULATOR_BIN=/absolute/path/to/aws-emulator
export GITOPS_REPO=https://github.com/psentaur/flashkart.git
export GITOPS_REVISION=your-kind-deployment-branch
export FLASHKART_ADMIN_TOKEN=local-admin
export FLASHKART_CUSTOMER_TOKEN=local-customer
make kind-up kind-images gitops-bootstrap
kubectl --context kind-flashkart -n argocd get applications
kubectl --context kind-flashkart -n flashkart get pods,hpa
```

For Podman set `CONTAINER_ENGINE=podman` and `KIND_EXPERIMENTAL_PROVIDER=podman` before building and creating the cluster. The rehearsal reached Podman's 2,048-process limit on the kind node. If the node uses that limit, raise it before bootstrap with `podman update --pids-limit 8192 "${KIND_CLUSTER:-flashkart}-control-plane"`. Bootstrap installs Argo CD and registers Applications for Metrics Server, the Flink operator, the emulator and marketplace. It initializes local queues, tables and Kubernetes Secrets with a signed loopback endpoint. Inside the cluster a pod-local tunnel keeps signed emulator requests on loopback from the application's perspective. The app sync runs migration and topic setup jobs before rolling out the business services. PostgreSQL backends use distinct StatefulSets and PVCs, and each replica agent uses its own SQL role credentials and fixed Service address.

```sh
REPORT_DIR=build/kubernetes/recovery make test-kubernetes
REPORT_DIR=build/kubernetes/load make test-load
kubectl --context kind-flashkart -n flashkart port-forward svc/grafana 13000:3000
```

Run recovery before load on a clean cluster. The recovery test checks Argo CD self-healing, replaces each agent pod and reads a previously written product after each replacement, then submits a duplicate purchase attempt and waits for confirmation. The load test creates a fresh limited-stock sale, runs catalog and purchase scenarios concurrently for three minutes, samples both HPAs, and checks stock conservation in PostgreSQL. It can leave a backlog that delays later checkouts. Set `DURATION`, `CATALOG_VUS`, or `ADMISSION_VUS` in the environment to adjust load. Reports go to `build/kubernetes` unless `REPORT_DIR` is set.

Open the Flashkart dashboard in Grafana at localhost:13000 using the local default credentials `admin/admin`. Use Explore with Loki and `{job="flashkart"}` for centralized logs. Replica SQL failures and failed peer RPCs have separate metrics. Flink emits watermark and late-event metrics and stores checkpoints on the shared volume.

For the backend-outage check on a disposable cluster, scale `statefulset/postgres-a` to zero. Port-forward directly to the replica A pod and check `/healthz`, `/readyz`, and `/metrics` while making catalog reads. Restore the StatefulSet to one replica and wait for readiness before testing the peer route. For the peer check, temporarily pause automated sync for the marketplace Application, change only the gRPC target port on `svc/replica-a` from 7001 to 7002, and restart replica B so it opens a fresh connection. Check replica B's failed peer sync counter and replica A's readiness. Restore port 7001 and automated sync after the check.

Regenerate base resources after migrations change with `python3 deploy/kubernetes/generate.py`. Regenerate dashboard resources with `python3 deploy/kubernetes/observe.py`. Both generated manifests are tracked so Argo CD can render them from a plain checkout.

## Local validation

See the [Phase 13A rehearsal report](phase-13a-verification.md) for the GitOps, recovery, load, and telemetry evidence.
