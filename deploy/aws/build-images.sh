#!/bin/sh
set -eu
: "${ARTIFACT_REPOSITORY:?Set the immutable ECR repository URI}"
: "${IMAGE_TAG:?Set the source revision used for image tags}"
: "${AWS_REGION:?Set the artifact repository region}"
engine=${CONTAINER_ENGINE:-docker}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
aws ecr get-login-password --region "$AWS_REGION" | "$engine" login --username AWS --password-stdin "${ARTIFACT_REPOSITORY%%/*}"
app="$ARTIFACT_REPOSITORY:app-$IMAGE_TAG"
"$engine" build --platform linux/amd64 -f deploy/kubernetes/app.Dockerfile -t "$app" .
mkdir "$work/notification" "$work/flink"
printf '%s\n' '{"environment":"aws","kafka":{"auth":"iam"}}' >"$work/notification/config.json"
"$engine" build --platform linux/amd64 --build-arg "APP_IMAGE=$app" -f deploy/aws/notification.Dockerfile -t "$ARTIFACT_REPOSITORY:notification-$IMAGE_TAG" "$work/notification"
cp analytics/target/sales-analytics-0.1.0.jar "$work/flink/"
mv "$work/flink/sales-analytics-0.1.0.jar" "$work/flink/sales-analytics.jar"
curl -fsSL https://repo.maven.apache.org/maven2/software/amazon/msk/aws-msk-iam-auth/2.3.5/aws-msk-iam-auth-2.3.5-all.jar -o "$work/flink/aws-msk-iam-auth.jar"
"$engine" build --platform linux/amd64 --build-arg EMR_FLINK_IMAGE=public.ecr.aws/emr-on-eks/flink/emr-7.13.0-flink:20260410 \
    -f deploy/aws/flink.Dockerfile -t "$ARTIFACT_REPOSITORY:flink-$IMAGE_TAG" "$work/flink"
"$engine" build --platform linux/amd64 -f deploy/kubernetes/web.Dockerfile -t "$ARTIFACT_REPOSITORY:web-$IMAGE_TAG" .
"$engine" build --platform linux/amd64 -f deploy/aws/migrations.Dockerfile -t "$ARTIFACT_REPOSITORY:migrations-$IMAGE_TAG" .
for image in app notification flink web migrations; do
    "$engine" push "$ARTIFACT_REPOSITORY:$image-$IMAGE_TAG"
done
