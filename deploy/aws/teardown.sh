#!/bin/sh
set -eu
test "${CONFIRM_TEARDOWN:-}" = flashkart || {
    echo 'Set CONFIRM_TEARDOWN=flashkart to destroy the disposable stack selected by Terraform state.' >&2
    exit 2
}
tf() { terraform -chdir=deploy/aws/environment "$@"; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
settings=$(tf output -json deployment)
export KUBECONFIG="$work/kubeconfig"
name=$(printf '%s' "$settings" | jq -er .name)
region=$(printf '%s' "$settings" | jq -er .region)
operator_role=$(printf '%s' "$settings" | jq -r '.operator_role // empty')
if [ -n "$operator_role" ]; then
    aws eks update-kubeconfig --name "$name" --region "$region" --role-arn "$operator_role" >/dev/null
else
    aws eks update-kubeconfig --name "$name" --region "$region" >/dev/null
fi
if kubectl get crd applications.argoproj.io --ignore-not-found -o name | grep -q applications; then
    kubectl -n argocd delete application flashkart-cloud --ignore-not-found --wait --timeout=15m
    kubectl -n argocd delete application cloud-flink-operator cloud-lb-controller cloud-metrics-server \
        --ignore-not-found --wait --timeout=15m
fi
# Bootstrap state and ECR are outside this Terraform root.
tf destroy "$@"
