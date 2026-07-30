.PHONY: build test test-race test-shuffle prove fmt fmt-check vet lint govulncheck check ci

# TAGS — extra build tags threaded through build/vet/lint/test, e.g.
# `make test TAGS=nodaemon` to exercise the daemon-free configuration.
# RACE — set to test with the race detector, e.g. `make test RACE=1`.
# SHUFFLE — set to randomize test order, e.g. `make test SHUFFLE=1`.
TAGS ?=
RACE ?=
SHUFFLE ?=
GOFLAGS_TAGS := $(if $(TAGS),-tags "$(TAGS)",)
GOFLAGS_TEST = $(if $(RACE),-race,) $(if $(SHUFFLE),-shuffle=on,) $(GOFLAGS_TAGS)

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

# Run the test suite. Honors RACE=1 (race detector) and SHUFFLE=1 (random test
# order); `test-race` and `test-shuffle` are the named entrypoints CI uses.
test:
	go test $(GOFLAGS_TEST) ./...

# Race detector over the whole suite. Slow and CGO-heavy (duckdb), so this is a
# separate target/job rather than part of `check` — the fast gate stays fast.
test-race:
	@$(MAKE) test RACE=1

# Randomized test order. Catches order-dependent tests that share package state.
test-shuffle:
	@$(MAKE) test SHUFFLE=1

# Show that a test fails when the code it guards is reverted or mutated. A test
# that passes against the pre-fix source guards nothing. Never uses `git stash`:
# in a shared checkout that would take a co-worker's uncommitted work with it.
#
#   make prove ARGS="--rev HEAD~1 --run TestFoo ./pkg/ pkg/a.go"
#
# Run `tools/prove --help` for the mutation and file-drop modes.
prove:
	@tools/prove $(ARGS)

# Full gate: build, format check, lint, vulncheck, test.
check: build fmt-check lint govulncheck test

# CI entrypoint: identical to the full gate.
ci: check
