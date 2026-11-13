#!/bin/sh
set -eu
kube() { "${KUBECTL:-kubectl}" --context "kind-${KIND_CLUSTER:-flashkart}" -n flashkart "$@"; }
work=$(mktemp -d)
kube port-forward svc/emulator 19297:9297 >"$work/forward.log" 2>&1 &
forward=$!
trap 'kill "$forward" 2>/dev/null || true; rm -rf "$work"' EXIT
for name in ca.pem emulator_credentials; do
    kube exec emulator-0 -- cat "/data/.aws/emulator/9297/$name" >"$work/$name"
done
aws_local() {
    env -i PATH="$PATH" AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE="$work/emulator_credentials" \
        AWS_CA_BUNDLE="$work/ca.pem" AWS_EC2_METADATA_DISABLED=true AWS_MAX_ATTEMPTS=1 \
        aws --region us-east-1 --endpoint-url https://127.0.0.1:19297 "$@"
}
n=0
until curl -fsS --cacert "$work/ca.pem" https://127.0.0.1:19297/__admin/health >/dev/null; do
    n=$((n + 1))
    test "$n" -lt 15
    sleep 1
done
test "$(aws_local sts get-caller-identity --query Account --output text)" = 000000000000
for queue in purchases outcomes; do aws_local sqs create-queue --queue-name "$queue" >/dev/null; done
for spec in 'notifications customer_id attempt_id' 'analytics sale_id starts_at'; do
    set -- $spec
    if ! aws_local dynamodb describe-table --table-name "$1" >/dev/null 2>&1; then
        aws_local dynamodb create-table --table-name "$1" --billing-mode PAY_PER_REQUEST \
            --attribute-definitions "AttributeName=$2,AttributeType=S" "AttributeName=$3,AttributeType=S" \
            --key-schema "AttributeName=$2,KeyType=HASH" "AttributeName=$3,KeyType=RANGE" >/dev/null
    fi
    aws_local dynamodb wait table-exists --table-name "$1"
done
kube create secret generic emulator-files --from-file="$work/ca.pem" --from-file="$work/emulator_credentials" \
    --dry-run=client -o yaml | kube apply -f -
kube create secret generic emulator-settings \
    --from-literal=FLASHKART_PURCHASE_QUEUE_URL=https://127.0.0.1:9297/000000000000/purchases \
    --from-literal=FLASHKART_OUTCOME_QUEUE_URL=https://127.0.0.1:9297/000000000000/outcomes \
    --from-literal=FLASHKART_SQS_CREDENTIALS_FILE=/emulator/emulator_credentials \
    --from-literal=FLASHKART_SQS_CA_FILE=/emulator/ca.pem \
    --from-literal=FLASHKART_DYNAMODB_CREDENTIALS_FILE=/emulator/emulator_credentials \
    --from-literal=FLASHKART_DYNAMODB_CA_FILE=/emulator/ca.pem \
    --from-literal=FLASHKART_NOTIFICATION_TABLE=notifications \
    --from-literal=FLASHKART_ANALYTICS_TABLE=analytics \
    --dry-run=client -o yaml | kube apply -f -
