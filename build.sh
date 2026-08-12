#!/usr/bin/env bash

set -eou pipefail

BUILD_DIR="build"

[[ " $* " =~ " --debug " ]] && DEBUG=true || DEBUG=false
GO_BUILD_FLAGS=()

COMMIT_HASH=$(git rev-parse HEAD)
BUILD_TIME=$(date -u -Iseconds)

LDFLAGS_VARS="-X 'main.CommitHash=${COMMIT_HASH}' -X 'main.BuildTime=${BUILD_TIME}'"

if [ "$DEBUG" = true ]; then
    GO_BUILD_FLAGS+=("-gcflags=all=-N -l")
    BASE_LDFLAGS="${LDFLAGS_VARS}"
else
    GO_BUILD_FLAGS+=("-trimpath")
    BASE_LDFLAGS="-s -w ${LDFLAGS_VARS}"
fi

ARCH="${1:-amd64}"
GOARCH=""

case "$ARCH" in
    arm64)
        GOARCH="arm64"
    ;;
    *)
        GOARCH="amd64"
    ;;
esac

BUILD_DIR_FULL="$BUILD_DIR/$GOARCH"

mkdir -p "$BUILD_DIR_FULL"
shopt -s nullglob
files=("$BUILD_DIR_FULL"/*)
shopt -u nullglob

if [ ${#files[@]} -gt 0 ]; then
    rm -rf "${files[@]}"
fi

case "$GOARCH" in
    arm64)
        if [ "$DEBUG" = true ]; then
            CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
                go build "${GO_BUILD_FLAGS[@]}" -ldflags "${BASE_LDFLAGS} -linkmode external -extldflags -static" -o "$BUILD_DIR_FULL/cli" ./cmd/cli.go
        else
            CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
                go build "${GO_BUILD_FLAGS[@]}" -ldflags "${BASE_LDFLAGS} -linkmode external -extldflags -static" -o "$BUILD_DIR_FULL/cli" ./cmd/cli.go
        fi
    ;;
    *)
        go build "${GO_BUILD_FLAGS[@]}" -ldflags "${BASE_LDFLAGS}" -o "$BUILD_DIR_FULL/cli" ./cmd/cli.go
    ;;
esac
