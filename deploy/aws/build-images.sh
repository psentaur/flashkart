#!/bin/sh
set -eu
umask 077
: "${ARTIFACT_REPOSITORY:?Set the immutable ECR repository URI}"
: "${IMAGE_TAG:?Set the source revision used for image tags}"
: "${AWS_REGION:?Set the artifact repository region}"
scope=${FLASHKART_IMAGE_SCOPE:-all}
case "$scope" in
    all) : "${VITE_CLERK_PUBLISHABLE_KEY:?Set the production Clerk publishable key or FLASHKART_IMAGE_SCOPE=backend}";;
    backend) ;;
    *) echo 'FLASHKART_IMAGE_SCOPE must be all or backend.' >&2; exit 2;;
esac
engine=${CONTAINER_ENGINE:-docker}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# Tags are immutable: a later frontend build must not republish backend tags.
aws ecr list-images --region "$AWS_REGION" --repository-name "${ARTIFACT_REPOSITORY#*/}" \
    --filter tagStatus=TAGGED --query 'imageIds[].imageTag' --output text >"$work/existing-raw"
tr '\t' '\n' <"$work/existing-raw" >"$work/existing"
exists() { grep -Fxq -- "$1-$IMAGE_TAG" "$work/existing"; }
publish() { "$engine" push "$ARTIFACT_REPOSITORY:$1-$IMAGE_TAG"; }
aws ecr get-login-password --region "$AWS_REGION" >"$work/login-password"
"$engine" login --username AWS --password-stdin "${ARTIFACT_REPOSITORY%%/*}" <"$work/login-password"
rm "$work/login-password"
app="$ARTIFACT_REPOSITORY:app-$IMAGE_TAG"
if ! exists app; then
    "$engine" build --platform linux/amd64 -f deploy/kubernetes/app.Dockerfile -t "$app" .
    publish app
fi
if ! exists notification; then
    mkdir "$work/notification"
    printf '%s\n' '{"environment":"aws","kafka":{"auth":"iam"}}' >"$work/notification/config.json"
    set --
    if [ "${engine##*/}" != podman ]; then set -- --provenance=false; fi
    "$engine" build "$@" --platform linux/amd64 --build-arg "APP_IMAGE=$app" -f deploy/aws/notification.Dockerfile -t "$ARTIFACT_REPOSITORY:notification-$IMAGE_TAG" "$work/notification"
    publish notification
fi
if ! exists flink; then
    mkdir "$work/flink"
    cp analytics/target/sales-analytics-0.1.0.jar "$work/flink/sales-analytics.jar"
    "$engine" build --platform linux/amd64 --build-arg EMR_FLINK_IMAGE=public.ecr.aws/emr-on-eks/flink/emr-7.13.0-flink:20260410 \
        -f deploy/aws/flink.Dockerfile -t "$ARTIFACT_REPOSITORY:flink-$IMAGE_TAG" "$work/flink"
    publish flink
fi
if ! exists migrations; then
    "$engine" build --platform linux/amd64 -f deploy/aws/migrations.Dockerfile -t "$ARTIFACT_REPOSITORY:migrations-$IMAGE_TAG" .
    publish migrations
fi
if [ "$scope" = all ]; then
    if ! exists web; then
        "$engine" build --platform linux/amd64 --build-arg VITE_AUTH_MODE=clerk --build-arg "VITE_CLERK_PUBLISHABLE_KEY=$VITE_CLERK_PUBLISHABLE_KEY" -f deploy/kubernetes/web.Dockerfile -t "$ARTIFACT_REPOSITORY:web-$IMAGE_TAG" .
        publish web
    fi
else
    echo 'Backend images ready. Frontend image deferred until Clerk is configured.'
fi
