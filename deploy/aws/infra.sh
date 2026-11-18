#!/usr/bin/env bash
# Infrastructure lifecycle wrapper. Configuration files are trusted Bash files.
set -Eeuo pipefail
umask 077
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
usage() {
    cat <<'HELP'
Usage: bash deploy/aws/infra.sh <bootstrap|images|plan|create|destroy> [--config FILE] [--yes]

  bootstrap  Create the shared Terraform state bucket and ECR repository.
  images     Build/push application images using the existing image builder.
  plan       Preview application infrastructure changes without applying them.
  create     Create/update application infrastructure (notification image required).
  destroy    Remove application infrastructure, cleaning up Argo applications first.

Configuration defaults to .env.cloud in the repository root; environment variables
also work. Copy deploy/aws/infra.env.example to .env.cloud to get started.
--yes bypasses interactive confirmation. Shared bootstrap resources are retained.
This script provisions infrastructure; use make cloud-render and make demo to deploy.
HELP
}
fail() { printf '%s\n' "$*" >&2; exit 2; }
[[ $# -gt 0 ]] || { usage; exit 2; }
action=$1; shift
case "$action" in -h|--help|help) usage; exit 0;; bootstrap|images|plan|create|destroy) ;; *) fail "Unknown action: $action";; esac
config="$ROOT/.env.cloud"
automatic=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --config) [[ $# -ge 2 ]] || fail '--config requires a file'; config=$2; [[ -f "$config" ]] || fail "Config file not found: $config"; shift 2;;
        --yes) automatic=true; shift;;
        *) fail "Unknown option: $1";;
    esac
done
if [[ -f "$config" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$config"
    set +a
fi
cd "$ROOT"
for command in terraform aws jq; do command -v "$command" >/dev/null || fail "Install $command first."; done
: "${AWS_ACCOUNT_ID:?Set AWS_ACCOUNT_ID in .env.cloud}"
: "${AWS_REGION:=ap-south-1}"
: "${ARTIFACT_NAME:=flashkart-artifacts}"
[[ "$AWS_ACCOUNT_ID" =~ ^[0-9]{12}$ ]] || fail 'AWS_ACCOUNT_ID must contain 12 digits.'
[[ "$ARTIFACT_NAME" =~ ^[a-z][a-z0-9-]{2,30}$ ]] || fail 'ARTIFACT_NAME must be 3-31 lowercase letters, digits, or hyphens.'
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION" TF_IN_AUTOMATION=1
actual=$(aws sts get-caller-identity --query Account --output text)
[[ "$actual" == "$AWS_ACCOUNT_ID" ]] || fail "AWS account mismatch: authenticated to $actual, expected $AWS_ACCOUNT_ID."
export TF_VAR_account_id="$AWS_ACCOUNT_ID" TF_VAR_region="$AWS_REGION"
export TF_VAR_artifact_name="$ARTIFACT_NAME"
boot() { terraform -chdir="$ROOT/deploy/aws/bootstrap" "$@"; }
env_tf() { terraform -chdir="$ROOT/deploy/aws/environment" "$@"; }
approve_destroy() {
    printf 'Destroy environment %s in account %s (%s), including application data.\n' "$FLASHKART_CLOUD_NAME" "$AWS_ACCOUNT_ID" "$AWS_REGION"
    if ! "$automatic"; then
        [[ -t 0 ]] || fail 'Destruction requires an interactive terminal or --yes.'
        read -r -p "Type $FLASHKART_CLOUD_NAME to continue: " answer
        [[ "$answer" == "$FLASHKART_CLOUD_NAME" ]] || fail 'Destruction cancelled.'
    fi
}
if [[ "$action" == bootstrap ]]; then
    boot init -input=false
    previous=$(boot output -json)
    if jq -e 'has("state_bucket")' <<<"$previous" >/dev/null; then
        jq -e --arg bucket "$ARTIFACT_NAME-$AWS_ACCOUNT_ID-tfstate" \
            '.state_bucket.value == $bucket' <<<"$previous" >/dev/null || fail 'Bootstrap state belongs to another account or artifact name.'
    fi
    args=()
    if "$automatic"; then args+=(-auto-approve); fi
    boot apply -input="$(if "$automatic"; then printf false; else printf true; fi)" "${args[@]}"
    printf 'Bootstrap ready. Next: infra.sh images, then infra.sh create.\n'
    exit 0
fi
# Explicit overrides support an existing shared bootstrap managed elsewhere.
if [[ -z "${STATE_BUCKET:-}" || -z "${ARTIFACT_REPOSITORY:-}" ]]; then
    boot init -input=false
    : "${STATE_BUCKET:=$(boot output -raw state_bucket)}"
    : "${ARTIFACT_REPOSITORY:=$(boot output -raw artifact_repository)}"
fi
[[ -n "$STATE_BUCKET" && -n "$ARTIFACT_REPOSITORY" ]] || fail 'Run infra.sh bootstrap first, or supply STATE_BUCKET and ARTIFACT_REPOSITORY.'
: "${IMAGE_TAG:?Set IMAGE_TAG to the immutable image revision to deploy}"
[[ "$IMAGE_TAG" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$ ]] || fail 'IMAGE_TAG must be an image tag of at most 100 characters.'
[[ "$ARTIFACT_REPOSITORY" == "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/"* ]] || fail 'ARTIFACT_REPOSITORY must be in the configured AWS account and region.'
export ARTIFACT_REPOSITORY IMAGE_TAG
if [[ "$action" == images ]]; then
    if [[ -z "${FLASHKART_IMAGE_SCOPE:-}" ]]; then
        if [[ -n "${VITE_CLERK_PUBLISHABLE_KEY:-}" ]]; then
            export FLASHKART_IMAGE_SCOPE=all
        else
            export FLASHKART_IMAGE_SCOPE=backend
        fi
    fi
    case "$FLASHKART_IMAGE_SCOPE" in
        backend)
            printf 'Building backend images for infrastructure creation. Frontend deferred until a Clerk key is configured.\n'
            make cloud-images
            ;;
        all)
            : "${VITE_CLERK_PUBLISHABLE_KEY:?Set the production Clerk publishable key or FLASHKART_IMAGE_SCOPE=backend}"
            make cloud-images build-web
            ;;
        *) fail 'FLASHKART_IMAGE_SCOPE must be all or backend.';;
    esac
    exit 0
fi
: "${FLASHKART_CLOUD_NAME:?Set FLASHKART_CLOUD_NAME}"
: "${EKS_VERSION:?Set EKS_VERSION to the Kubernetes version to deploy}"
: "${OPERATOR_CIDRS:?Set OPERATOR_CIDRS to a JSON array of restricted operator CIDRs}"
[[ "$FLASHKART_CLOUD_NAME" =~ ^[a-z][a-z0-9-]{2,19}$ ]] || fail 'FLASHKART_CLOUD_NAME must be 3-20 lowercase letters, digits, or hyphens.'
jq -e 'type == "array" and length > 0 and all(.[]; type == "string" and (endswith("/0") | not))' <<<"$OPERATOR_CIDRS" >/dev/null || fail 'OPERATOR_CIDRS must be a nonempty JSON array without /0 networks.'
export TF_VAR_name="$FLASHKART_CLOUD_NAME" TF_VAR_eks_version="$EKS_VERSION"
export TF_VAR_operator_cidrs="$OPERATOR_CIDRS" TF_VAR_artifact_repository="$ARTIFACT_REPOSITORY" TF_VAR_image_tag="$IMAGE_TAG"
if [[ -n "${DATABASE_AVAILABILITY_ZONE:-}" ]]; then
    export TF_VAR_database_availability_zone="$DATABASE_AVAILABILITY_ZONE"
fi
: "${STATE_KEY:=flashkart/$FLASHKART_CLOUD_NAME.tfstate}"
[[ "${TF_WORKSPACE:-default}" == default ]] || fail 'This script uses the default Terraform workspace with a separate STATE_KEY per environment.'
export TF_WORKSPACE=default
env_tf init -input=false -reconfigure \
    -backend-config="bucket=$STATE_BUCKET" -backend-config="region=$AWS_REGION" \
    -backend-config="key=$STATE_KEY" -backend-config=use_lockfile=true
selected=$(env_tf output -json)
if jq -e 'has("deployment")' <<<"$selected" >/dev/null; then
    jq -e --arg name "$FLASHKART_CLOUD_NAME" --arg region "$AWS_REGION" \
        '.deployment.value.name == $name and .deployment.value.region == $region' <<<"$selected" >/dev/null || fail 'Selected Terraform state belongs to another environment or region.'
fi
case "$action" in
    plan) env_tf plan -input=false;;
    create)
        repository_name=${ARTIFACT_REPOSITORY#*/}
        # Listing succeeds for an empty repository; permission/auth failures stop execution.
        images=$(aws ecr list-images --region "$AWS_REGION" --repository-name "$repository_name" --filter tagStatus=TAGGED --output json)
        jq -e --arg tag "notification-$IMAGE_TAG" 'any(.imageIds[]; .imageTag == $tag)' <<<"$images" >/dev/null || fail "Missing notification-$IMAGE_TAG image. Run infra.sh images first."
        args=()
        if "$automatic"; then args+=(-auto-approve); fi
        env_tf apply -input="$(if "$automatic"; then printf false; else printf true; fi)" "${args[@]}"
        printf 'Infrastructure ready. Render manifests with make cloud-render; deployment instructions are in docs/phase-12-development.md.\n'
        elastic_ips=$(env_tf output -json elastic_ips)
        printf '\nNAT gateway Elastic IPs (outbound traffic):\n'
        jq -r 'if length == 0 then "  (none)" else .[] | "  " + . end' <<<"$elastic_ips"
        deployment=$(env_tf output -json deployment)
        website_url=$(jq -er '.public_url' <<<"$deployment")
        printf '\nWebsite URL: %s\n' "$website_url"
        printf 'Use this HTTPS URL in your browser. NAT Elastic IPs do not serve the website.\n'
        printf 'Application deployment is separate: render manifests, deploy services, and publish the frontend as described in docs/phase-12-development.md.\n'
        ;;
    destroy)
        approve_destroy
        clusters=$(aws eks list-clusters --region "$AWS_REGION" --output json)
        if jq -e --arg name "$FLASHKART_CLOUD_NAME" '.clusters | index($name) != null' <<<"$clusters" >/dev/null; then
            command -v kubectl >/dev/null || fail 'Install kubectl to clean up the deployed controllers before destruction.'
            CONFIRM_TEARDOWN=flashkart sh deploy/aws/teardown.sh -input=false -auto-approve
        else
            # Also handles a partially applied environment that never created EKS.
            env_tf destroy -input=false -auto-approve
        fi
        printf 'Application environment destroyed. Shared state bucket and ECR repository retained.\n'
        ;;
esac
