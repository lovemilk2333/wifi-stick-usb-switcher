# wifi-stick-usb-switcher build entry
#
# libusbgx (shared .so) is built with meson and rebuilt automatically
# when its sources change; the cli targets link the per-arch libusbgx
# (soname libusbgx.so.2, already shipped on the production device).
#
# Usage:
#   make cli-amd64            build for the host, output build/amd64/cli
#   make cli-arm64            cross build, output build/arm64/cli
#   make DEBUG=1 cli-amd64    build with debug info (-gcflags=all=-N -l)

SHELL := /usr/bin/env bash

BUILD_DIR := build
LIBUSBGX_DIR := core/usb/gadget/libusbgx

# libusbgx sources (meson.build + C + headers); any change triggers rebuild
LIBUSBGX_SRC := $(LIBUSBGX_DIR)/meson.build \
	$(wildcard $(LIBUSBGX_DIR)/src/*.c) \
	$(wildcard $(LIBUSBGX_DIR)/src/function/*.c) \
	$(wildcard $(LIBUSBGX_DIR)/include/usbg/*.h) \
	$(wildcard $(LIBUSBGX_DIR)/include/usbg/function/*.h)

LIBUSBGX_SONAME := libusbgx.so.2.0.0
MESON_OPTS := --buildtype=release -Dgadget-schemes=disabled -Dtests=disabled -Ddoxygen=disabled -Dwerror=false

ARM64_CROSS_FILE := $(BUILD_DIR)/libusbgx/aarch64-cross.txt

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

# ---- libusbgx -------------------------------------------------------------

# meson setup runs once (no build.ninja yet); later visits only reconfigure
build/libusbgx/amd64/$(LIBUSBGX_SONAME): $(LIBUSBGX_SRC)
	mkdir -p $(dir $@)
	@if [ ! -f $(dir $@)build.ninja ]; then \
		meson setup $(dir $@) $(LIBUSBGX_DIR) $(MESON_OPTS); \
	else \
		meson configure $(dir $@) $(MESON_OPTS); \
	fi
	ninja -C $(dir $@)

# meson machine files want single quotes around values
build/libusbgx/arm64/$(LIBUSBGX_SONAME): $(LIBUSBGX_SRC)
	mkdir -p $(dir $@)
	@if [ ! -f $(ARM64_CROSS_FILE) ]; then \
		printf '[binaries]\nc = '\''aarch64-linux-gnu-gcc'\''\ncpp = '\''aarch64-linux-gnu-g++'\''\nar = '\''aarch64-linux-gnu-ar'\''\nstrip = '\''aarch64-linux-gnu-strip'\''\n\n[host_machine]\nsystem = '\''linux'\''\ncpu_family = '\''aarch64'\''\ncpu = '\''aarch64'\''\nendian = '\''little'\''\n' > $(ARM64_CROSS_FILE); \
	fi
	@if [ ! -f $(dir $@)build.ninja ]; then \
		meson setup $(dir $@) $(LIBUSBGX_DIR) $(MESON_OPTS) --cross-file $(ARM64_CROSS_FILE); \
	else \
		meson configure $(dir $@) $(MESON_OPTS); \
	fi
	ninja -C $(dir $@)

# ---- cli ------------------------------------------------------------------

.PHONY: all cli-amd64 cli-arm64 clean

all: cli-amd64

cli-amd64: build/libusbgx/amd64/$(LIBUSBGX_SONAME)
	mkdir -p build/amd64
	CGO_ENABLED=1 CGO_LDFLAGS="-L$(abspath build/libusbgx/amd64) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" -ldflags "$(BASE_LDFLAGS)" -o build/amd64/cli ./cmd/cli.go

# arm64:zig cc 交叉到 glibc 2.31(设备 Debian 的 glibc 太老,开发机动态
# glibc 符号版本不可用;glibc 静态 + 依赖 libc 的动态库会双 libc 冲突)。
# 链接用上游 meson 自编的 libusbgx(符号集合一致,CI 无设备也能构建);
# 运行时用设备预装的 HandsomeMod 特制 .so(soname 同为 libusbgx.so.2,
# 动态链接运行期解析,不打包不分发)。
ZIG := $(HOME)/tools/zig/zig
ZIG_TARGET := aarch64-linux-gnu.2.31

cli-arm64: build/libusbgx/arm64/$(LIBUSBGX_SONAME)
	mkdir -p build/arm64
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(ZIG) cc -target $(ZIG_TARGET)" \
		CGO_LDFLAGS="-L$(abspath build/libusbgx/arm64) -lusbgx" \
		go build "$(GO_BUILD_FLAGS)" \
		-ldflags "$(BASE_LDFLAGS) -linkmode external" \
		-o build/arm64/cli ./cmd/cli.go

clean:
	rm -rf build
