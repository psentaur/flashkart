#!/bin/sh
set -eu
: "${GITOPS_REPO:?Set GITOPS_REPO to the repository URL reachable by Argo CD}"
: "${GITOPS_REVISION:?Set GITOPS_REVISION to the branch containing the deployed image tags}"
: "${FLASHKART_ADMIN_TOKEN:?Set the local admin token}"
: "${FLASHKART_CUSTOMER_TOKEN:?Set the local customer token}"
if printf '%s\n' "$GITOPS_REVISION" | grep -Eq '^[[:xdigit:]]{40}$'; then
    revision=$(printf '%s' "$GITOPS_REVISION" | tr 'A-F' 'a-f')
else
    revision=$(git ls-remote --exit-code "$GITOPS_REPO" "$GITOPS_REVISION" "$GITOPS_REVISION^{}" |
        awk '$2 ~ /\^\{\}$/ { peeled=$1; next } { sha=$1; refs++ }
             END { if (refs == 1) print (peeled != "" ? peeled : sha); else exit 1 }') || {
        echo 'GITOPS_REVISION must resolve to one Git ref or be a full commit SHA.' >&2
        exit 2
    }
fi
export FLASHKART_GITOPS_COMMIT="$revision"
kube() { "${KUBECTL:-kubectl}" --context "kind-${KIND_CLUSTER:-flashkart}" "$@"; }

kube create namespace argocd --dry-run=client -o yaml | kube apply -f -
kube apply --server-side -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.1.8/manifests/install.yaml
kube wait -n argocd --for=condition=Established crd/applications.argoproj.io --timeout=180s
kube create namespace flashkart --dry-run=client -o yaml | kube apply -f -
kube -n flashkart create secret generic demo-tokens \
    --from-literal=FLASHKART_ADMIN_TOKEN="$FLASHKART_ADMIN_TOKEN" \
    --from-literal=FLASHKART_CUSTOMER_TOKEN="$FLASHKART_CUSTOMER_TOKEN" \
    --from-literal=FLASHKART_CUSTOMER_ID=kind-buyer --dry-run=client -o yaml | kube apply -f -
kube -n argocd rollout status deployment/argocd-repo-server --timeout=300s
kube apply -f deploy/kubernetes/platform/argocd-flink.yaml

application() {
    python3 - "$1" "$2" "$3" <<'PY' | kube apply -f -
import json, os, sys
name, path, namespace = sys.argv[1:]
print(json.dumps({
    "apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
    "metadata": {"name": name, "namespace": "argocd"},
    "spec": {"project": "default",
        "ignoreDifferences": [{"group": "apps", "kind": "StatefulSet",
            "jqPathExpressions": [".spec.volumeClaimTemplates[] | .status"]}],
        "source": {"repoURL": os.environ["GITOPS_REPO"], "targetRevision": os.environ["FLASHKART_GITOPS_COMMIT"], "path": path},
        "destination": {"server": "https://kubernetes.default.svc", "namespace": namespace},
        "syncPolicy": {"automated": {"prune": True, "selfHeal": True}, "syncOptions": ["ServerSideApply=true"]}},
}))
PY
}
application metrics-server deploy/kubernetes/platform/metrics-server kube-system
application flashkart-emulator deploy/kubernetes/platform flashkart
deadline=0
until kube -n flashkart get statefulset emulator >/dev/null 2>&1; do
    deadline=$((deadline + 1))
    test "$deadline" -lt 90
    sleep 2
done
kube -n flashkart rollout status statefulset/emulator --timeout=300s
sh deploy/kubernetes/emulator-bootstrap.sh
kube wait --for=condition=Established crd/flinkdeployments.flink.apache.org --timeout=300s
kube -n argocd wait --for=jsonpath='{.status.operationState.phase}'=Succeeded application/flink-operator --timeout=300s
kube -n argocd wait --for=jsonpath='{.status.health.status}'=Healthy application/flink-operator --timeout=300s
application flashkart deploy/kubernetes/kind flashkart

for _ in $(seq 1 180); do
    if kube -n argocd get application/flashkart -o json | jq -e \
        --arg revision "$revision" --arg repo "$GITOPS_REPO" '
        .status.sync as $sync |
        $sync.revision == $revision and $sync.status == "Synced" and
        $sync.comparedTo.source.targetRevision == $revision and
        $sync.comparedTo.source.repoURL == $repo and
        $sync.comparedTo.source.path == "deploy/kubernetes/kind" and
        .status.health.status == "Healthy"' >/dev/null; then
        exit 0
    fi
    sleep 5
done
echo "Timed out waiting for flashkart at $revision" >&2
exit 1
