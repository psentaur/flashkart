#!/bin/sh
set -eu
settings=$(terraform -chdir=deploy/aws/environment output -json deployment)
bucket=$(printf '%s' "$settings" | jq -er .buckets.frontend)
region=$(printf '%s' "$settings" | jq -er .region)
test -f web/dist/index.html
aws s3 sync web/dist/assets/ "s3://$bucket/assets/" --region "$region" --cache-control public,max-age=31536000,immutable
aws s3 cp web/dist/index.html "s3://$bucket/index.html" --region "$region" --cache-control no-cache,max-age=0
