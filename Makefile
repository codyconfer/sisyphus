.PHONY: build test fmt fmt-check vet lint govulncheck check ci

# TAGS — extra build tags threaded through build/vet/lint/test, e.g.
# `make test TAGS=nodaemon` to exercise the daemon-free configuration.
TAGS ?=
GOFLAGS_TAGS := $(if $(TAGS),-tags "$(TAGS)",)

# Build all packages.
build:
	go build $(GOFLAGS_TAGS) ./...

# Tooling lives in ./tools (separate module) so consumers don't inherit linter deps.
GO_TOOL = go tool -modfile=tools/go.mod

# Format all Go source in place (gofmt + goimports via golangci-lint).
fmt:
	$(GO_TOOL) golangci-lint fmt

# Verify all Go source is formatted; fail (showing the diff) if not.
fmt-check:
	$(GO_TOOL) golangci-lint fmt --diff

# go vet: the standard toolchain analyzers.
vet:
	go vet $(GOFLAGS_TAGS) ./...

# golangci-lint: aggregate static analysis (govet, staticcheck, errcheck, ...).
lint:
	$(GO_TOOL) golangci-lint run $(if $(TAGS),--build-tags "$(TAGS)",)

# govulncheck: report known vulnerabilities in dependencies and reachable code.
govulncheck:
	$(GO_TOOL) govulncheck $(GOFLAGS_TAGS) ./...

# Run the test suite.
test:
	go test $(GOFLAGS_TAGS) ./...

# Full gate: build, format check, lint, vulncheck, test.
check: build fmt-check lint govulncheck test

# CI entrypoint: identical to the full gate.
ci: check
