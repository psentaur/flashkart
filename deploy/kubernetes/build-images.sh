#!/bin/sh
set -eu
: "${IMAGE_TAG:?Set IMAGE_TAG to an immutable build identifier}"
: "${AWS_EMULATOR_BIN:?Set AWS_EMULATOR_BIN to the installed emulator binary}"
engine=${CONTAINER_ENGINE:-docker}
kind=${KIND:-kind}
cluster=${KIND_CLUSTER:-flashkart}
context=$(mktemp -d)
trap 'rm -rf "$context"' EXIT
mkdir "$context/emulator"
cp "$AWS_EMULATOR_BIN" "$context/emulator/aws-emulator"
for image in app web flink proxy emulator; do
    dockerfile="deploy/kubernetes/$image.Dockerfile"
    source=.
    if [ "$image" = emulator ]; then
        dockerfile=deploy/kubernetes/platform/emulator.Dockerfile
        source=$context/emulator
    fi
    reference="docker.io/library/flashkart-$image:$IMAGE_TAG"
    set --
    if [ "$image" = web ]; then set -- --build-arg VITE_AUTH_MODE=demo; fi
    "$engine" build "$@" -f "$dockerfile" -t "$reference" "$source"
    "$engine" save -o "$context/image.tar" "$reference"
    "$kind" load image-archive --name "$cluster" "$context/image.tar"
    rm "$context/image.tar"
done
