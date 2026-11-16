#!/bin/sh
set -eu
mode=$1
: "${FLASHKART_ADMIN_TOKEN:?Set the local admin token}"
: "${FLASHKART_CUSTOMER_TOKEN:?Set the local customer token}"
kube() { "${KUBECTL:-kubectl}" --context "kind-${KIND_CLUSTER:-flashkart}" -n flashkart "$@"; }
reports=${REPORT_DIR:-build/kubernetes}
mkdir -p "$reports"
kube port-forward svc/web :8080 >"$reports/port-forward.log" 2>&1 &
forward=$!
sampler=
trap 'kill "$forward" ${sampler:-} 2>/dev/null || true' EXIT
n=0
while :; do
    port=$(sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9]*\) -> 8080$/\1/p' "$reports/port-forward.log")
    if [ -n "$port" ] && curl -fsS "http://127.0.0.1:$port/" >/dev/null; then break; fi
    kill -0 "$forward"
    n=$((n + 1))
    test "$n" -lt 20
    sleep 1
done
BASE_URL="http://127.0.0.1:$port"
export BASE_URL
if [ "$mode" = load ]; then
    RUN_ID="kind-$(date +%s)"
    SALE_ID=$(python3 tests/kubernetes-market.py sale "$RUN_ID")
    case "$SALE_ID" in ''|*[!a-zA-Z0-9_-]*) exit 2;; esac
    export RUN_ID SALE_ID
    (
        while :; do
            kube get hpa -o json
            sleep 5
        done
    ) >"$reports/hpa.jsonstream" &
    sampler=$!
    k6 run --summary-export "$reports/k6.json" tests/kubernetes-load.js
    kill "$sampler"
    sampler=
    python3 tests/kubernetes-market.py hpa "$reports/hpa.jsonstream" >"$reports/hpa-peaks.json"
    cat "$reports/hpa-peaks.json"
    kube exec postgres-transactional-0 -- psql -U flashkart_admin -d flashkart -At \
        -c "SELECT available_stock >= 0 AND available_stock + (SELECT count(*) FROM inventory.reservations WHERE sale_id='$SALE_ID' AND state IN ('HELD','COMMITTED')) = (terms->>'stock')::bigint FROM inventory.sales WHERE sale_id='$SALE_ID'" >"$reports/stock-conserved.txt"
    grep -qx t "$reports/stock-conserved.txt"
else
    test "$mode" = recovery
    kube get pods,hpa >"$reports/before.txt"
    kube patch svc/web --type=json -p='[{"op":"replace","path":"/spec/ports/0/targetPort","value":8081}]'
    for _ in $(seq 1 90); do
        test "$(kube get svc/web -o jsonpath='{.spec.ports[0].targetPort}')" = 8080 && break
        sleep 2
    done
    test "$(kube get svc/web -o jsonpath='{.spec.ports[0].targetPort}')" = 8080
    echo "Argo CD restored the web service target port" >"$reports/self-heal.txt"
    product="recovery-$(date +%s)"
    python3 tests/kubernetes-market.py product "$product"
    for replica in a b c d; do
        old=$(kube get pod -l "app=replica-$replica" -o jsonpath='{.items[0].metadata.uid}')
        kube delete pod -l "app=replica-$replica" --wait
        kube rollout status "deployment/replica-$replica" --timeout=120s
        new=$(kube get pod -l "app=replica-$replica" -o jsonpath='{.items[0].metadata.uid}')
        test "$old" != "$new"
        python3 tests/kubernetes-market.py read "$product"
    done
    sale=$(python3 tests/kubernetes-market.py sale "$product")
    python3 tests/kubernetes-market.py purchase "$sale" >"$reports/checkout.json"
    kube get pods,hpa >"$reports/after.txt"
    kube wait --for=jsonpath='{.status.jobStatus.state}'=RUNNING flinkdeployment/sales-analytics --timeout=180s
fi
