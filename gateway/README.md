# Maintaining the generated client

This document is for maintainers of the library. If you only want to *use*
it, see the [root README](../README.md) and the
[API reference](https://pkg.go.dev/github.com/airlock/gateway-rest-api-lib-go/gateway).

The package is a thin hand-written layer over a client generated with
[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) from the
gateway's own OpenAPI specification: `spec/active.json`, which is a copy of
one of the vendored specs under `spec/versions/`.

## Layout

| Path | Contents |
|------|----------|
| `gateway/gateway.go` | session handling (Bearer key + `JSESSIONID` cookie), TLS and client options, `APIError` / `CheckStatus`, the two `//go:generate` directives |
| `gateway/helpers.go` | `Expect*` status helpers, `ToOne` / `ToMany` |
| `gateway/gateway.gen.go` | **generated** by oapi-codegen: all models and client methods |
| `gateway/version_gen.go` | **generated** by `internal/genversion`: `GeneratedFromVersion` |
| `gateway/oapi-codegen.yaml` | generator configuration |
| `gateway/gateway_test.go` | offline tests (`httptest`) |
| `gateway/helpers_test.go` | offline tests for the status helpers |
| `gateway/integration_test.go` | real-gateway smoke tests (build tag `integration`) |
| `spec/active.json` | the spec the client is generated from; a byte-identical copy of one file in `spec/versions/`. The `//go:generate` directives point here and never change. |
| `spec/versions/` | vendored OpenAPI specs, one file per gateway version |
| `internal/genversion/` | generates `version_gen.go` from a spec's `info.version` |
| `examples/` | runnable demo commands, see [examples/README.md](../examples/README.md) |
| `.github/` | GitHub workflows |

## Setup

```sh
go mod tidy          # runtime dep + the pinned generator (tool directive)
make test
```

`make check` is the full local gate: it regenerates, gofmt-checks, vets, runs
`staticcheck` and the race-enabled test suite, and then fails if the
regenerated files differ from what is committed.

The race detector needs a C compiler. Without one, `make test` and
`make check` fail with "-race requires cgo"; run them with `RACE=` to skip
the race detector locally, for example `make check RACE=`. CI always runs
with it.

## Regenerating

The generator is pinned via go.mod's `tool` directive (Go 1.24+), so no
install step is needed and everyone regenerates with the same version:

```sh
# vendor a fresh spec from your gateway (no auth required, see https://docs.airlock.com/gateway/latest/index/1579105144099.html):
curl -k https://<gateway>/airlock/rest/v3/api-docs > spec/versions/api-docs-8.7.0.json

# activate it, then regenerate:
cp spec/versions/api-docs-8.7.0.json spec/active.json
make check
```

`spec/active.json` is the single source of truth for which version the client
is generated from, and activating a version is a file copy — the two
`//go:generate` directives in `gateway.go` point at that fixed path and never
need editing. One runs `go tool oapi-codegen` with the config in
`oapi-codegen.yaml`, the other runs `internal/genversion` to refresh
`version_gen.go`, whose `GeneratedFromVersion` comes from the spec's own
`info.version` — so it follows the copy automatically.

`make checkactive` (part of `make check`) fails if `spec/active.json` is not a
byte-identical copy of a file in `spec/versions/`, so an edited or unvendored
active spec cannot go unnoticed. You can also invoke the generator manually:

```sh
cd gateway && go tool oapi-codegen -config oapi-codegen.yaml ../spec/active.json
```

Regeneration is supported from **8.5.1 onward**. The hand-written layer does
not compile against older specs.

To bump the generator version, keep the runtime in step with it — newer
generators may emit calls to newer runtime helpers:

```sh
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
go get github.com/oapi-codegen/runtime@latest
go mod tidy && make check
```

Currently pinned: oapi-codegen v2.8.0, runtime v1.8.0 (module targets Go 1.27).

## Generated naming

Names follow Go initialism conventions (`SSLCertificateDto`, `Data.ID`,
`GetSSLCertificatesWithResponse`), not the spec's own casing. This comes from
`name-normalizer: ToCamelCaseWithInitialisms` plus the
`additional-initialisms` list in `oapi-codegen.yaml` — extend that list when a
gateway release introduces an acronym it does not yet cover.

## Why the client tracks the newest spec

The client is generated from the newest supported gateway release and works
against older ones implicitly: unknown JSON fields are ignored when reading,
and optional fields are pointers with `omitempty`, so write bodies only contain
attributes the caller set. Talking to an older gateway works as long as the
caller does not touch attributes that do not exist there.

Reasoning why there is not one client per gateway version: the API renames operations across
releases, so each version would need its own copy of the hand-written layer,
with every fix backported to each of them. Releases therefore follow semantic
versioning of the Go API, and the gateway release a version was generated from
is recorded as `GeneratedFromVersion`, not in the tag name.

## Testing

Testing has two layers: automated checks that run on every change, and a
manual test against a real gateway.

### Continuous integration

CI is a GitHub Actions workflow (`.github/workflows/ci.yml`). It runs on
every push to `main` and on every pull request. It runs `make check` twice:
with the Go version from `go.mod` (the minimum the module supports) and with
the current stable Go release. It then runs `govulncheck`.

`make check` runs, in order:

1. **`checkactive`** — fails unless `spec/active.json` is identical to a file
   in `spec/versions/`. The client is always generated from a vendored spec.
2. **`generate`** — regenerates `gateway.gen.go` and `version_gen.go` from the
   active spec.
3. **`fmt`, `vet`, `staticcheck`** — formatting and static analysis.
   `staticcheck` skips the generated file because of its `DO NOT EDIT` header.
4. **`test`** — the unit tests with the race detector.
5. **Drift check** — fails if the regenerated files differ from the committed
   ones. A spec change cannot be merged without its generated code.

This is also the compatibility check: if a gateway release renames or removes
a generated identifier that `gateway.go` uses, step 4 fails to compile.

### Manual testing against a real gateway

`integration_test.go` (build tag `integration`) tests the client against a
real gateway. It is not part of the normal build or of CI; run it manually
against a test gateway:

```sh
TEST_HOST=gateway.example.com:8443 TEST_API_KEY=eyJ... make integration
```

| variable | effect |
|----------|--------|
| `TEST_HOST` | host, optionally with port, no scheme. Required. |
| `TEST_API_KEY` | REST API key. Required. Without both, the tests skip. |
| `TEST_EXPECT_VERSION` | e.g. `8.6`. Fails unless the gateway reports this major.minor. Guards against a wrong host. |

The tests use `WithInsecureSkipVerify`, so a self-signed test certificate is
fine. The file contains two tests:

- **`TestIntegrationRead`** connects, loads the active configuration and
  reads virtual hosts, mappings and certificates.
- **`TestIntegrationWrite`** creates a mapping from a system template, saves
  the configuration and deletes that saved configuration again. It never
  activates, so the running configuration is untouched.

`make integration` runs both. To run one, set `RUN`, for example
`make integration RUN=TestIntegrationRead`.

## Upgrading to a new gateway version

1. Vendor the new spec:

   ```sh
   curl -k https://<gateway>/airlock/rest/v3/api-docs > spec/versions/api-docs-8.7.0.json
   ```

2. Review what changed BEFORE activating it:

   ```sh
   make specdiff OLD=spec/active.json NEW=spec/versions/api-docs-8.7.0.json
   ```

   Breaking changes listed here are the ones that may rename or remove
   generated identifiers used by `gateway.go`.
3. Activate it and regenerate:

   ```sh
   cp spec/versions/api-docs-8.7.0.json spec/active.json
   make check
   ```

4. Fix any compile errors in the hand-written files — the compiler lists
   exactly what the gateway release renamed or removed.
5. Verify against a real gateway of the new release:

   ```sh
   TEST_HOST=gateway.example.com:8443 \
   TEST_API_KEY=eyJ... \
   TEST_EXPECT_VERSION=8.7 \
   make integration
   ```

   This runs both tests; see [Testing](#testing) for what each does. `TEST_EXPECT_VERSION` fails the run unless the gateway
   reports that major.minor, so a wrong host cannot pass quietly.
6. Keep the previous spec in `spec/versions/` so `make specdiff` has something
   to compare against; drop the ones before it.
7. Update the version named in the compatibility section of the
   [root README](../README.md). After the pull request is merged, tag the
   merge commit on the main branch and push the tag:

   ```sh
   git checkout main && git pull
   git tag v1.3.0
   git push origin v1.3.0
   ```

   Tags are ordinary semantic versions of the Go API; the gateway release is
   recorded in `GeneratedFromVersion`, not in the tag name. Optionally, name
   it in an annotated tag message as well:

   ```sh
   git tag -a v1.3.0 -m "Generated from Airlock Gateway 8.6.0"
   ```

   If a gateway release ever breaks the *Go* API for consumers, bump the
   module's major version (`/v2`). Go requires the major version in the
   module path from v2 onward, so a `v2.0.0` tag only works together with
   changing the module path in `go.mod` to `.../gateway-rest-api-lib-go/v2`
   and updating all imports, including in consumers. A breaking change to the
   hand-written Go API is therefore a project-wide change, not just a tag.
