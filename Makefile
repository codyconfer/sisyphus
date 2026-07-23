.PHONY: build fmt fmt-check vet staticcheck govulncheck lint test check ci

# Build all packages.
build:
	go build ./...

# Format all Go source in place.
fmt:
	gofmt -w .

# Verify all Go source is formatted; fail (listing offenders) if not.
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
	  echo "gofmt needed on:"; echo "$$out"; exit 1; \
	fi

# go vet: the standard toolchain analyzers.
vet:
	go vet ./...

# staticcheck: open-source static analysis (honnef.co/go/tools).
staticcheck:
	go tool staticcheck ./...

# govulncheck: report known vulnerabilities in dependencies and reachable code.
govulncheck:
	go tool govulncheck ./...

# All static analysis: formatting + vet + staticcheck + govulncheck.
lint: fmt-check vet staticcheck govulncheck

# Run the test suite.
test:
	go test ./...

# Full gate: build, lint, test.
check: build lint test

# CI entrypoint: identical to the full gate.
ci: check
