#!/bin/sh
# Shared tool bootstrap; source after defining ROOT.
ensure_kubectl() {
    if command -v kubectl >/dev/null 2>&1; then return; fi
    FLASHKART_TOOLS_DIR=${FLASHKART_TOOLS_DIR:-"$ROOT/build/cloud-tools"}
    mkdir -p "$FLASHKART_TOOLS_DIR"
    FLASHKART_TOOLS_DIR=$(cd "$FLASHKART_TOOLS_DIR" && pwd)
    PATH="$FLASHKART_TOOLS_DIR:$PATH"
    export PATH
    if command -v kubectl >/dev/null 2>&1; then return; fi
    (
        set -eu
        version=${FLASHKART_KUBECTL_VERSION:-v1.35.10}
        case "$(uname -s)" in Linux) platform=linux;; Darwin) platform=darwin;; *) echo 'Install kubectl for this operating system.' >&2; exit 2;; esac
        case "$(uname -m)" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo 'Install kubectl for this CPU architecture.' >&2; exit 2;; esac
        command -v curl >/dev/null || { echo 'Install curl to download kubectl.' >&2; exit 2; }
        if ! command -v sha256sum >/dev/null && ! command -v shasum >/dev/null; then
            echo 'Install sha256sum or shasum to verify kubectl.' >&2; exit 2
        fi
        work=$(mktemp -d "$FLASHKART_TOOLS_DIR/.kubectl.XXXXXX")
        trap 'rm -rf "$work"' 0
        printf 'Downloading verified kubectl %s to %s (no sudo required).\n' "$version" "$FLASHKART_TOOLS_DIR"
        url="https://dl.k8s.io/release/$version/bin/$platform/$arch/kubectl"
        curl -fsSL --connect-timeout 15 --max-time 180 "$url" -o "$work/kubectl"
        curl -fsSL --connect-timeout 15 --max-time 60 "$url.sha256" -o "$work/checksum"
        if command -v sha256sum >/dev/null; then actual=$(sha256sum "$work/kubectl"); else actual=$(shasum -a 256 "$work/kubectl"); fi
        actual=${actual%% *}
        expected=$(cat "$work/checksum")
        [ "$actual" = "$expected" ] || { echo 'kubectl checksum mismatch; refusing to execute it.' >&2; exit 2; }
        chmod 755 "$work/kubectl"
        mv "$work/kubectl" "$FLASHKART_TOOLS_DIR/kubectl"
    )
}
