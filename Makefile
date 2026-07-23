.PHONY: build test fmt fmt-check vet lint govulncheck check ci

# Build all packages.
build:
	go build ./...

# Format all Go source in place (gofmt + goimports via golangci-lint).
fmt:
	go tool golangci-lint fmt

# Verify all Go source is formatted; fail (showing the diff) if not.
fmt-check:
	go tool golangci-lint fmt --diff

# go vet: the standard toolchain analyzers.
vet:
	go vet ./...

# golangci-lint: aggregate static analysis (govet, staticcheck, errcheck, ...).
lint:
	go tool golangci-lint run

# govulncheck: report known vulnerabilities in dependencies and reachable code.
govulncheck:
	go tool govulncheck ./...

# Run the test suite.
test:
	go test ./...

# Full gate: build, format check, lint, vulncheck, test.
check: build fmt-check lint govulncheck test

# CI entrypoint: identical to the full gate.
ci: check
