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
#   make cli-amd64            build for the host, output build/amd64/cli
#   make cli-arm64            cross build, output build/arm64/cli
#   make DEBUG=1 cli-amd64    build with debug info (-gcflags=all=-N -l)

SHELL := /usr/bin/env bash

BUILD_DIR := build

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

# zig cc cross-compiles to glibc 2.31 for the device (its glibc is older
# than the dev machine's; fully static glibc cannot coexist with a dynamic
# libusbgx.so.2 — cgo needs a glibc-2.31-targeting C compiler).
ZIG := $(HOME)/tools/zig/zig
ZIG_TARGET := aarch64-linux-gnu.2.31

# empty implementations, one per exported symbol name
$(STUB_SRC): $(SYMBOLS)
	mkdir -p $(dir $@)
	awk '{ print "void " $$0 "(void) {}" }' $(SYMBOLS) > $@

build/libusbgx/stub/amd64/libusbgx.so: $(STUB_SRC)
	mkdir -p $(dir $@)
	cc -fPIC -shared -Wl,-soname,libusbgx.so.2 -o $@ $(STUB_SRC)
	ln -sf libusbgx.so $(dir $@)libusbgx.so.2

build/libusbgx/stub/arm64/libusbgx.so: $(STUB_SRC)
	mkdir -p $(dir $@)
	$(ZIG) cc -target $(ZIG_TARGET) -fPIC -shared -Wl,-soname,libusbgx.so.2 -o $@ $(STUB_SRC)
	ln -sf libusbgx.so $(dir $@)libusbgx.so.2

# ---- cli ------------------------------------------------------------------

.PHONY: all cli-amd64 cli-arm64 clean

all: cli-amd64

cli-amd64: build/libusbgx/stub/amd64/libusbgx.so
	mkdir -p build/amd64
	CGO_ENABLED=1 CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/amd64) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" -ldflags "$(BASE_LDFLAGS)" -o build/amd64/cli ./cmd/cli.go

cli-arm64: build/libusbgx/stub/arm64/libusbgx.so
	mkdir -p build/arm64
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(ZIG) cc -target $(ZIG_TARGET)" \
		CGO_LDFLAGS="-L$(abspath build/libusbgx/stub/arm64) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" \
		-ldflags "$(BASE_LDFLAGS) -linkmode external" \
		-o build/arm64/cli ./cmd/cli.go

clean:
	rm -rf build
