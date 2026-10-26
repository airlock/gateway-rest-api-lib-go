# Build, lint, and test tasks. CI invokes the same targets.

STATICCHECK_VERSION := 2026.2.2
OASDIFF_VERSION := v1.34.1

# The race detector needs cgo and therefore a C compiler. On a machine
# without one, run e.g. `make check RACE=` to skip it. CI always uses it.
RACE ?= -race

.PHONY: generate fmt vet staticcheck test cover check checkactive specdiff integration

generate:
	go generate ./...

# Fail if any tracked Go file is not gofmt-formatted (git ls-files keeps this
# out of the in-repo module cache).
fmt:
	@unformatted=$$(gofmt -l $$(git ls-files '*.go')); \
		if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi

vet:
	go vet ./...

# Skips gateway.gen.go, which carries the "DO NOT EDIT" header.
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

test:
	go test $(RACE) ./...

# Print coverage of the hand-written code, excluding gateway.gen.go.
cover:
	go test $(RACE) -coverprofile=coverage.out ./...
	grep -v 'gateway/gateway.gen.go' coverage.out > coverage.hand.out || true
	@go tool cover -func=coverage.hand.out | tail -1

# Verify spec/active.json is a byte-identical copy of a file in spec/versions/.
# Switch version with:
#   cp spec/versions/api-docs-<version>.json spec/active.json && make check
checkactive:
	@match=""; \
	for f in spec/versions/api-docs-*.json; do \
		if cmp -s spec/active.json "$$f"; then match="$$f"; break; fi; \
	done; \
	if [ -z "$$match" ]; then \
		echo "ERROR: spec/active.json matches no file in spec/versions/."; \
		echo "       Activate a vendored spec with:"; \
		echo "         cp spec/versions/api-docs-<version>.json spec/active.json"; \
		exit 1; \
	fi; \
	echo "active spec: $$match"

# Regenerate, lint, test, and fail if the committed generated code is stale.
check: checkactive generate fmt vet staticcheck test
	@git diff --exit-code --stat -- gateway/*.gen.go gateway/version_gen.go \
		|| (echo "ERROR: generated code is out of sync with spec/active.json; commit the regenerated files" && exit 1)

# List breaking changes between two specs, e.g.:
#   make specdiff OLD=spec/versions/api-docs-8.5.1.json NEW=spec/active.json
# Use "changelog" in place of "breaking" for all changes.
specdiff:
	go run github.com/oasdiff/oasdiff@$(OASDIFF_VERSION) breaking $(OLD) $(NEW)

# Run the integration tests against a real gateway. Both tests run; narrow
# with RUN, e.g. RUN=TestIntegrationRead.
#   TEST_HOST=gateway.example.com:8443 TEST_API_KEY=... make integration
RUN ?= TestIntegration
integration:
	go test -tags integration ./gateway/ -run $(RUN) -v -count=1
