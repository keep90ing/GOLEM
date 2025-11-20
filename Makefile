# Makefile for the gocker project

# Go parameters
# 讓腳本可以讀取這個路徑
export GO_EXECUTABLE ?= /usr/local/go/bin/go
GO_FLAGS = -v

# Binary names
CLI_BIN = gocker
DAEMON_BIN = gocker-daemon

# Source paths
CMD_PATH = ./cmd
CLI_MAIN = $(CMD_PATH)/gocker
DAEMON_MAIN = $(CMD_PATH)/gocker-daemon

# Default target executed when you just run `make`
all: build

# New target for checking dependencies
check-deps:
	@echo "--> Checking build dependencies..."
	@bash ./scripts/check_deps.sh

# Build all binaries
build: build-cli build-daemon

build-cli:
	@echo "--> Building Gocker CLI..."
	@$(GO_EXECUTABLE) build $(GO_FLAGS) -o $(CLI_BIN) $(CLI_MAIN)

build-daemon:
	@echo "--> Building Gocker Daemon (statically linked)..."
	@CGO_ENABLED=0 $(GO_EXECUTABLE) build $(GO_FLAGS) -ldflags '-s -w -extldflags "-static"' -o $(DAEMON_BIN) $(DAEMON_MAIN)

# Install the gocker system
install:
	@echo "--> Installing Gocker system-wide (Need root privilege)..."
	@sudo bash ./scripts/install.sh

# Uninstall the gocker system
uninstall:
	@echo "--> Uninstalling Gocker system-wide (Need root privilege)..."
	@sudo bash ./scripts/uninstall.sh

# Clean up build artifacts
clean:
	@echo "--> Cleaning up..."
	@rm -f $(CLI_BIN) $(DAEMON_BIN)

# Systemd service management shortcuts
start:
	@sudo systemctl start gocker-daemon.service

stop:
	@sudo systemctl stop gocker-daemon.service

restart:
	@sudo systemctl restart gocker-daemon.service

status:
	@sudo systemctl status gocker-daemon.service

logs:
	@sudo journalctl -u gocker-daemon.service -f

# Phony targets are not actual files
.PHONY: all build build-cli build-daemon install uninstall clean start stop restart status logs check-deps
