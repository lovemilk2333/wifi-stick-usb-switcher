BUILD_DIR := build
BINARY_NAME := caddy-tls-ask

ifneq ($(filter debug,$(MAKECMDGOALS)),)
    DEBUG := true
else
    DEBUG := false
endif

ifneq ($(filter arm64,$(MAKECMDGOALS)),)
    GOARCH := arm64
else
    GOARCH := amd64
endif

TARGET_DIR := $(BUILD_DIR)/$(GOARCH)
OUTPUT_BIN := $(TARGET_DIR)/$(BINARY_NAME)

ifeq ($(DEBUG),true)
    GO_BUILD_FLAGS := -gcflags=all=-N -l
    BASE_LDFLAGS :=
else
    GO_BUILD_FLAGS := -trimpath
    BASE_LDFLAGS := -s -w
endif

.PHONY: all build clean help amd64 arm64 debug

all: build

build: clean
	@mkdir -p $(TARGET_DIR)
	@if [ "$(GOARCH)" = "arm64" ]; then \
		CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC=aarch64-linux-gnu-gcc \
		go build $(GO_BUILD_FLAGS) -ldflags "$(BASE_LDFLAGS) -linkmode external -extldflags -static" -o $(OUTPUT_BIN) ./main.go; \
	else \
		go build $(GO_BUILD_FLAGS) -ldflags "$(BASE_LDFLAGS)" -o $(OUTPUT_BIN) ./main.go; \
	fi

amd64 arm64 debug:
	@:

clean:
	@rm -rf $(BUILD_DIR)/*

help:
	@echo "Usage:"
	@echo "  make build [amd64|arm64] [debug]"
