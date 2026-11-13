#!/bin/sh
set -eu
: "${REPLICA:?Set REPLICA to a, b, c, or d}"
case "$REPLICA" in a|b|c|d) ;; *) exit 2;; esac
: "${CONFIRM_OUTAGE:?Set CONFIRM_OUTAGE to the disposable Terraform environment name}"
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
fixture="outage-$(date +%s)"
python3 tests/kubernetes-market.py read-ready "$REPLICA"
rds stop-db-instance >/dev/null
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
wait_stopped
python3 tests/kubernetes-market.py read-unready "$REPLICA" >"$reports/replica-outage.json"
python3 tests/kubernetes-market.py product "$fixture"
python3 tests/kubernetes-market.py read "$fixture"
recover
trap - EXIT
python3 tests/kubernetes-market.py read-ready "$REPLICA" >"$reports/replica-recovered.json"
python3 tests/kubernetes-market.py read "$fixture"
