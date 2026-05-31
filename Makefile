.PHONY: build install uninstall test vet

PREFIX ?= $(HOME)/.local
BIN_DIR := $(PREFIX)/bin
BIN_NAME := agmsg

# リリース (.goreleaser.yaml) と同じ version 文字列をローカルビルドにも注入する。
# git 情報が無い環境では "dev" にフォールバックする。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ishii1648/agmsg-go/internal/cli.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BIN_NAME) ./cmd/agmsg/

# ソースから直接ビルドして配置する。go ツールチェーンが生成したバイナリには
# com.apple.quarantine が付かないため、macOS でも Gatekeeper 警告なしで実行できる。
install:
	@mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BIN_NAME)" ./cmd/agmsg/
	@echo "Installed: $(BIN_DIR)/$(BIN_NAME)"
	@case ":$$PATH:" in *":$(BIN_DIR):"*) ;; *) echo "Warning: $(BIN_DIR) is not in PATH";; esac

uninstall:
	rm -f "$(BIN_DIR)/$(BIN_NAME)"
	@echo "Removed: $(BIN_DIR)/$(BIN_NAME)"

test:
	go test ./...

vet:
	go vet ./...
