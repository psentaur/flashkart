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
recover() {
    rds wait db-instance-stopped
    rds start-db-instance >/dev/null
    rds wait db-instance-available
}
fixture="outage-$(date +%s)"
python3 tests/kubernetes-market.py read-ready "$REPLICA"
rds stop-db-instance >/dev/null
trap 'recover' EXIT
rds wait db-instance-stopped
python3 tests/kubernetes-market.py read-unready "$REPLICA" >"$reports/replica-outage.json"
python3 tests/kubernetes-market.py product "$fixture"
python3 tests/kubernetes-market.py read "$fixture"
recover
trap - EXIT
python3 tests/kubernetes-market.py read-ready "$REPLICA" >"$reports/replica-recovered.json"
python3 tests/kubernetes-market.py read "$fixture"
