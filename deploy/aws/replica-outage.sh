#!/bin/sh
set -eu
: "${REPLICA:?Set REPLICA to a, b, c, or d}"
case "$REPLICA" in a|b|c|d) ;; *) exit 2;; esac
: "${CONFIRM_OUTAGE:?Set CONFIRM_OUTAGE to the disposable Terraform environment name}"
: "${FLASHKART_ADMIN_TOKEN:?Set the deployed admin token}"
tf() { terraform -chdir=deploy/aws/environment "$@"; }
settings=$(tf output -json deployment)
name=$(printf '%s' "$settings" | jq -er .name)
test "$CONFIRM_OUTAGE" = "$name"
region=$(printf '%s' "$settings" | jq -er .region)
BASE_URL=$(printf '%s' "$settings" | jq -er .public_url)
export BASE_URL REPLICA
database=$(tf output -json replica_databases | jq -er --arg replica "$REPLICA" '.[$replica]')
reports=${REPORT_DIR:-build/cloud}
mkdir -p "$reports"
rds() { aws rds --region "$region" "$@" --db-instance-identifier "$database"; }
db_status() {
    rds describe-db-instances --query 'DBInstances[0].DBInstanceStatus' --output text
}
wait_stopped() {
    for _ in $(seq 1 120); do
        if [ "$(db_status)" = stopped ]; then return 0; fi
        sleep 15
    done
    echo "Timed out waiting for $database to stop" >&2
    return 1
}
recover() {
    for _ in $(seq 1 120); do
        case "$(db_status)" in
            available) return 0 ;;
            stopped) rds start-db-instance >/dev/null || true ;;
        esac
        sleep 15
    done
    echo "Recovery could not confirm $database is available" >&2
    return 1
}
cleanup() {
    status=$?
    trap - EXIT
    if ! recover; then exit 1; fi
    exit "$status"
}
admin_get() {
    curl -fsS --max-time 10 -H "Authorization: Bearer $FLASHKART_ADMIN_TOKEN" "$BASE_URL$1"
}
pending_hints() {
    jq -er --arg fallback "$fallback" '.[] | select(.replica_id == $fallback and .backend_ready) | .pending_hints' "$1"
}
prefix="outage-$(date +%s)"
for n in $(seq 1 20); do
    fixture="$prefix-$n"
    owners=$(admin_get "/api/admin/replicas/products/$fixture/owners")
    if printf '%s' "$owners" | jq -e --arg replica "$REPLICA" 'index($replica) != null' >/dev/null; then
        break
    fi
done
fallback=$(printf '%s' "$owners" | jq -er --arg replica "$REPLICA" '
    select(length == 3 and index($replica) != null) | (["a","b","c","d"] - .)[0]')
printf '%s' "$owners" | jq --arg fixture "$fixture" --arg fallback "$fallback" \
    '{product_id:$fixture, preferred_owners:., fallback:$fallback}' >"$reports/replica-fixture.json"
python3 tests/kubernetes-market.py read-ready "$REPLICA" >"$reports/replica-baseline.json"
test "$(pending_hints "$reports/replica-baseline.json")" -eq 0
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
rds stop-db-instance >/dev/null
wait_stopped
python3 tests/kubernetes-market.py read-unready "$REPLICA" >"$reports/replica-outage.json"
python3 tests/kubernetes-market.py product "$fixture"
python3 tests/kubernetes-market.py read "$fixture"
admin_get /api/admin/replicas >"$reports/replica-hinted.json"
test "$(pending_hints "$reports/replica-hinted.json")" -gt 0
admin_get "/api/admin/replicas/products/$fixture" | jq -eS 'select(length == 1)' >"$reports/replica-version-outage.json"
recover
trap - EXIT
python3 tests/kubernetes-market.py read-ready "$REPLICA" >"$reports/replica-recovered.json"
for _ in $(seq 1 60); do
    admin_get /api/admin/replicas >"$reports/replica-drained.json"
    if [ "$(pending_hints "$reports/replica-drained.json")" = 0 ]; then break; fi
    sleep 2
done
test "$(pending_hints "$reports/replica-drained.json")" -eq 0
python3 tests/kubernetes-market.py read "$fixture"
admin_get "/api/admin/replicas/products/$fixture" | jq -eS 'select(length == 1)' >"$reports/replica-version-recovered.json"
cmp "$reports/replica-version-outage.json" "$reports/replica-version-recovered.json"
