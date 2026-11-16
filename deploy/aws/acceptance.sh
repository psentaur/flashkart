#!/bin/sh
set -eu
mode=$1
: "${FLASHKART_ADMIN_TOKEN:?Set the deployed admin token}"
: "${FLASHKART_CUSTOMER_TOKEN:?Set the deployed customer token}"
reports=${REPORT_DIR:-build/cloud}
mkdir -p "$reports"
settings=$(terraform -chdir=deploy/aws/environment output -json deployment)
BASE_URL=$(printf '%s' "$settings" | jq -er .public_url)
export BASE_URL
curl -fsS "$BASE_URL/" -o "$reports/index.html"
test "$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/api/admin/replicas")" = 401
run="cloud-$(date +%s)"
sale=$(python3 tests/kubernetes-market.py sale "$run")
printf '%s\n' "$sale" >"$reports/sale.txt"
python3 tests/kubernetes-market.py purchase "$sale" >"$reports/purchase.json"
if [ "$mode" = smoke ]; then exit; fi
test "$mode" = acceptance
work=$(mktemp -d)
sampler=
trap 'if [ -n "$sampler" ]; then kill "$sampler" 2>/dev/null || true; fi; rm -rf "$work"' EXIT
export KUBECONFIG="$work/kubeconfig"
name=$(printf '%s' "$settings" | jq -er .name)
region=$(printf '%s' "$settings" | jq -er .region)
repository=$(printf '%s' "$settings" | jq -er .repository)
image_tag=$(printf '%s' "$settings" | jq -er .tag)
git rev-parse HEAD >"$reports/source-revision.txt"
aws ecr describe-images --region "$region" --repository-name "${repository#*/}" \
    --query 'imageDetails[].{tags:imageTags,digest:imageDigest}' --output json --image-ids \
    "imageTag=app-$image_tag" "imageTag=notification-$image_tag" "imageTag=flink-$image_tag" \
    "imageTag=web-$image_tag" "imageTag=migrations-$image_tag" >"$reports/image-digests.json"
jq -e 'length == 5 and all(.[]; .digest | startswith("sha256:"))' "$reports/image-digests.json" >/dev/null
operator_role=$(printf '%s' "$settings" | jq -r '.operator_role // empty')
set --
if [ -n "$operator_role" ]; then set -- --role-arn "$operator_role"; fi
aws eks update-kubeconfig --name "$name" --region "$region" "$@" >/dev/null
kubectl -n flashkart get deployments,pods,hpa,flinkdeployments,targetgroupbindings -o wide >"$reports/workloads.txt"
kubectl -n argocd get applications -o wide >"$reports/gitops.txt"
kubectl -n argocd get application flashkart-cloud -o json |
    jq -er 'select(.status.sync.status == "Synced" and .status.health.status == "Healthy") | .status.sync.revision' \
    >"$reports/gitops-revision.txt"
kubectl -n flashkart get flinkdeployment sales-analytics -o jsonpath='{.status.jobStatus.state}' | grep -qx RUNNING
for _ in $(seq 1 30); do
    curl -fsS "$BASE_URL/api/analytics/sales/$sale" -o "$reports/analytics.json"
    if jq -e 'any(.[]; .confirmed_purchases > 0)' "$reports/analytics.json" >/dev/null; then break; fi
    sleep 5
done
jq -e 'any(.[]; .confirmed_purchases > 0)' "$reports/analytics.json" >/dev/null
(
    while :; do
        kubectl -n flashkart get hpa -o json
        sleep 5
    done
) >"$reports/hpa.jsonstream" &
sampler=$!
export RUN_ID="$run-load" SALE_ID="$sale"
k6 run --summary-export "$reports/k6.json" tests/kubernetes-load.js
kill "$sampler"
sampler=
python3 tests/kubernetes-market.py hpa "$reports/hpa.jsonstream" >"$reports/hpa-peaks.json"
cat "$reports/hpa-peaks.json"
aws logs describe-log-streams --region "$region" --log-group-name "$(printf '%s' "$settings" | jq -er .logs)" \
    --max-items 20 >"$reports/log-streams.json"
jq -e '.logStreams | length > 0' "$reports/log-streams.json" >/dev/null
aws logs describe-log-streams --region "$region" --log-group-name "/aws/lambda/$name-notification" \
    --max-items 20 >"$reports/lambda-streams.json"
jq -e '.logStreams | length > 0' "$reports/lambda-streams.json" >/dev/null
