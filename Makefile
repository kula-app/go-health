# ============================================================================
# GO-HEALTH MAKEFILE
# ============================================================================
# This Makefile provides automation for testing and developing the
# go-health library. Run 'make help' to see all available commands.
# ============================================================================

# Default target - show help when running 'make' without arguments
.DEFAULT_GOAL := help

# ============================================================================
# SETUP & INSTALLATION
# ============================================================================

## Initialize project for development (installs all dependencies)
#
# This command sets up your development environment by:
# - Installing system dependencies via Homebrew (if available)
# - Installing Go module dependencies
# - Preparing the project for development
#
# Run this once when you first clone the repository.
.PHONY: init
init:
	@if [ "$$(uname)" = "Darwin" ]; then \
		echo "Darwin detected."; \
		$(MAKE) init-darwin; \
	elif [ "$$(uname)" = "Linux" ]; then \
		echo "Linux detected."; \
		$(MAKE) init-linux; \
	else \
		echo "Not running on Darwin or Linux."; \
		exit 1; \
	fi
	$(MAKE) install

.PHONY: init-darwin
init-darwin:
	@if ! command -v brew >/dev/null 2>&1; then \
		echo "Homebrew not detected. Skipping system dependency installation."; \
		exit 1; \
	fi
	echo "Homebrew detected. Installing system dependencies..."; \
	brew bundle

.PHONY: init-linux
init-linux:
	@if ! command -v dprint >/dev/null 2>&1; then \
		echo "dprint not detected. Installing it using: curl -fsSL https://dprint.dev/install.sh | sh"; \
		exit 1; \
	fi

## Install and tidy Go module dependencies
#
# Downloads and installs all Go module dependencies and removes
# unused modules. Safe to run multiple times.
.PHONY: install
install:
	go mod tidy

# ============================================================================
# BUILDING
# ============================================================================

## Build all packages
#
# Compiles every package under the module. Library-only — produces no
# binary artifact, but catches type-check errors across the whole tree.
.PHONY: build
build:
	go build ./...

# ============================================================================
# TESTING & QUALITY ASSURANCE
# ============================================================================

## Run all tests in the project
#
# Executes all unit tests, integration tests, and benchmarks.
# Tests are run with Go's built-in testing framework with verbose output.
#
# Use 'make test-coverage' to generate coverage report.
# Use 'make test-race' to check for race conditions.
.PHONY: test
test:
	go test -v ./...

## Run tests with coverage report
#
# Executes all tests and generates a coverage report.
# Coverage data is saved to coverage.out and a summary is displayed.
.PHONY: test-coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

## Run tests with race detector
#
# Executes all tests with the race detector enabled to check for
# concurrent access issues and race conditions.
.PHONY: test-race
test-race:
	go test -v -race ./...

## Run comprehensive static analysis and security checks
#
# Performs multiple code quality checks:
# - go vet: Examines Go source code for suspicious constructs
# - staticcheck: Advanced static analysis for bugs and performance issues
# - govulncheck: Scans for known security vulnerabilities
#
# Fix any issues reported before committing code.
.PHONY: analyze
analyze:
	go vet ./...
	go tool staticcheck ./...
	go tool govulncheck ./...

## Format code and organize imports
#
# Automatically formats all code in the project:
# - go mod tidy: Cleans up module dependencies
# - go fmt: Formats Go source code to standard style
# - dprint fmt: Formats other files (JSON, YAML, etc.) using dprint
#
# Run this before committing to ensure consistent code style.
.PHONY: format
format:
	go mod tidy
	go fmt ./...
	dprint fmt

# ============================================================================
# MAINTENANCE
# ============================================================================

## Update all dependencies to latest compatible versions
#
# Updates all Go module dependencies to their latest minor/patch versions
# while respecting semantic versioning constraints. After updating:
# - Dependencies are updated to latest compatible versions
# - Code is automatically formatted
# - Module files are tidied
#
# Review changes carefully before committing dependency updates.
.PHONY: upgrade-deps
upgrade-deps:
	go get -u ./...
	$(MAKE) format

# ============================================================================
# HELP & DOCUMENTATION
# ============================================================================

## Show this help message with all available commands
#
# Displays a formatted list of all available make targets with descriptions.
# Commands are organized by topic for easy navigation.
.PHONY: help
help:
	@echo "=============================================="
	@echo "🩺 GO-HEALTH DEVELOPMENT COMMANDS"
	@echo "=============================================="
	@echo ""
	@awk 'BEGIN { desc = ""; target = "" } \
	/^## / { desc = substr($$0, 4) } \
	/^\.PHONY: / && desc != "" { \
		target = $$2; \
		printf "\033[36m%-20s\033[0m %s\n", target, desc; \
		desc = ""; target = "" \
	}' $(MAKEFILE_LIST)
	@echo ""
	@echo "💡 Use 'make <command>' to run any command above."
	@echo "📖 For detailed information, see comments in the Makefile."
	@echo ""
