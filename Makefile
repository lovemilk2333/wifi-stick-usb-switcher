# wifi-stick-usb-switcher build entry
#
# libusbgx is NOT vendored or compiled: declarations live in
# core/usb/gadget/usbg_min.h (hand-written ABI facts), and linking uses
# an empty stub .so generated from core/usb/gadget/libusbgx.symbols
# (function-name list exported from the device's libusbgx.so.2). The
# stub sets SONAME=libusbgx.so.2, so the binary loads the device's real
# library at runtime. The repo ships neither libusbgx source nor binary.
#
# Usage:
#   make cli-amd64                  build IPC transport for the host
#   make cli-arm64-5.15             cross build IPC transport (glibc 2.31)
#   make cli-arm64-latest           cross build IPC transport (glibc 2.41)
#   make cli-dbus-amd64             build D-Bus transport for the host
#   make cli-dbus-arm64-5.15        cross build D-Bus transport (glibc 2.31)
#   make cli-dbus-arm64-latest      cross build D-Bus transport (glibc 2.41)
#   make DEBUG=1 cli-amd64          build with debug info (-gcflags=all=-N -l)

SHELL := /usr/bin/env bash

BUILD_DIR := build

# `make` without a target builds every variant.
.DEFAULT_GOAL := all

# ---- version info ---------------------------------------------------------

COMMIT_HASH := $(shell git rev-parse HEAD)
BUILD_TIME := $(shell date -u -Iseconds)
LDFLAGS_VARS := -X 'main.CommitHash=$(COMMIT_HASH)' -X 'main.BuildTime=$(BUILD_TIME)'

ifdef DEBUG
GO_BUILD_FLAGS := -gcflags=all=-N -l
BASE_LDFLAGS := $(LDFLAGS_VARS)
else
GO_BUILD_FLAGS := -trimpath
BASE_LDFLAGS := -s -w $(LDFLAGS_VARS)
endif

# ---- libusbgx link stub ---------------------------------------------------

SYMBOLS := core/usb/gadget/libusbgx.symbols
STUB_SRC := build/libusbgx/stub/libusbgx_stub.c

# zig cc cross-compiles to the device glibc. 2.31 matches the 5.15 image;
# 2.41 is the newest glibc supported by zig 0.14.1 for newer images.
ZIG := $(HOME)/tools/zig/zig
ZIG_TARGET_5_15 := aarch64-linux-gnu.2.31
ZIG_TARGET_LATEST := aarch64-linux-gnu.2.41

# empty implementations, one per exported symbol name
$(STUB_SRC): $(SYMBOLS)
	mkdir -p $(dir $@)
	awk '{ print "void " $$0 "(void) {}" }' $(SYMBOLS) > $@

build/libusbgx/stub/amd64/libusbgx.so: $(STUB_SRC)
	mkdir -p $(dir $@)
	cc -fPIC -shared -Wl,-soname,libusbgx.so.2 -o $@ $(STUB_SRC)
	ln -sf libusbgx.so $(dir $@)libusbgx.so.2

build/libusbgx/stub/arm64-5.15/libusbgx.so: $(STUB_SRC)
	mkdir -p $(dir $@)
	$(ZIG) cc -target $(ZIG_TARGET_5_15) -fPIC -shared -Wl,-soname,libusbgx.so.2 -o $@ $(STUB_SRC)
	ln -sf libusbgx.so $(dir $@)libusbgx.so.2

build/libusbgx/stub/arm64-latest/libusbgx.so: $(STUB_SRC)
	mkdir -p $(dir $@)
	$(ZIG) cc -target $(ZIG_TARGET_LATEST) -fPIC -shared -Wl,-soname,libusbgx.so.2 -o $@ $(STUB_SRC)
	ln -sf libusbgx.so $(dir $@)libusbgx.so.2

# ---- cli ------------------------------------------------------------------

.PHONY: all all-dbus \
	cli-amd64 cli-arm64-5.15 cli-arm64-latest \
	cli-dbus-amd64 cli-dbus-arm64-5.15 cli-dbus-arm64-latest \
	clean

all: cli-amd64 cli-arm64-5.15 cli-arm64-latest \
	cli-dbus-amd64 cli-dbus-arm64-5.15 cli-dbus-arm64-latest

all-dbus: cli-dbus-amd64 cli-dbus-arm64-5.15 cli-dbus-arm64-latest

cli-amd64: build/libusbgx/stub/amd64/libusbgx.so
	mkdir -p build/amd64
	CGO_ENABLED=1 CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/amd64) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" -ldflags "$(BASE_LDFLAGS)" -o build/amd64/cli ./cmd/cli.go

cli-arm64-5.15: build/libusbgx/stub/arm64-5.15/libusbgx.so
	mkdir -p build/arm64-5.15
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(ZIG) cc -target $(ZIG_TARGET_5_15)" \
		CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/arm64-5.15) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" \
		-ldflags "$(BASE_LDFLAGS) -linkmode external" \
		-o build/arm64-5.15/cli ./cmd/cli.go

cli-arm64-latest: build/libusbgx/stub/arm64-latest/libusbgx.so
	mkdir -p build/arm64-latest
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(ZIG) cc -target $(ZIG_TARGET_LATEST)" \
		CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/arm64-latest) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" \
		-ldflags "$(BASE_LDFLAGS) -linkmode external" \
		-o build/arm64-latest/cli ./cmd/cli.go

cli-dbus-amd64: build/libusbgx/stub/amd64/libusbgx.so
	mkdir -p build/dbus-amd64
	CGO_ENABLED=1 CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/amd64) -lusbgx" \
		go build -tags dbus "$(GO_BUILD_FLAGS)" -ldflags "$(BASE_LDFLAGS)" -o build/dbus-amd64/cli ./cmd/cli.go

cli-dbus-arm64-5.15: build/libusbgx/stub/arm64-5.15/libusbgx.so
	mkdir -p build/dbus-arm64-5.15
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(ZIG) cc -target $(ZIG_TARGET_5_15)" \
		CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/arm64-5.15) -lusbgx" \
		go build -tags dbus "$(GO_BUILD_FLAGS)" \
		-ldflags "$(BASE_LDFLAGS) -linkmode external" \
		-o build/dbus-arm64-5.15/cli ./cmd/cli.go

cli-dbus-arm64-latest: build/libusbgx/stub/arm64-latest/libusbgx.so
	mkdir -p build/dbus-arm64-latest
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(ZIG) cc -target $(ZIG_TARGET_LATEST)" \
		CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/arm64-latest) -lusbgx" \
		go build -tags dbus "$(GO_BUILD_FLAGS)" \
		-ldflags "$(BASE_LDFLAGS) -linkmode external" \
		-o build/dbus-arm64-latest/cli ./cmd/cli.go

clean:
	rm -rf build
