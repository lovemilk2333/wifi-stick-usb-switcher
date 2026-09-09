#!/usr/bin/env bash

set -eou pipefail

ARGS=()

[[ " $* " =~ " --debug " ]] && ARGS+=(DEBUG=1)

ARCH="${1:-amd64}"
case "$ARCH" in
    arm64)
        ARGS+=(cli-arm64)
    ;;
    *)
        ARGS+=(cli-amd64)
    ;;
esac

exec make "${ARGS[@]}"
