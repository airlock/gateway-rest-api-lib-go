# Airlock Gateway Go Example Commands

Runnable example commands built on the Airlock Gateway Go client library.
Each command is intended solely as an **example** and is provided "as is"
without any warranty or official support — they are independent of one
another and are meant as a starting point for your own tooling. **Your use
of any command is at your own risk.**

| Command                | Purpose |
|------------------------|---------|
| `tutorial/`            | Guided walkthrough: config backup/export, create mappings from template, backend group, virtual host, validator round-trip, relationships, cleanup, import. Never activates. |
| `denyrulegroup/`       | `enable`/`disable` a deny rule group on all mappings. |
| `maintenance/`         | `show`/`enable`/`disable` the maintenance page on regex-selected mappings. |
| `setlogonly/`          | Set log-only mode on regex-selected deny rule groups for regex-selected mappings. |
| `iplists/`             | `list` IP address lists; `update` mappings to use one as blacklist (relationship) or path whitelist (ipRules). |
| `customdenyrule/`      | Create a custom deny rule with one restriction; create-or-reuse a custom deny rule group and connect them. |
| `denyruleexceptions/`  | `add`/`delete`/`list` exceptions on built-in deny rule groups per mapping. |
| `main.go`              | Minimal listing demo (virtual hosts, mappings). |
| `certrotation/`        | Rotate the certificate and private key of an ssl-certificate resource: compare deployed vs local PEM, replace both in one PATCH on difference. |
| `headerrewrite/`       | Add a custom Host-header rewrite and disable the built-in default Host rewrite on regex-selected mappings — using raw JSON request bodies (see below). |
| `internal/exutil/`     | Shared helpers: API key resolution (flag → `API_KEY` → `api_key.conf`), session setup with signal cleanup, confirm prompts, save/activate flow, mapping selection, request-body constructors (see below). |

Common conventions:

- The gateway host comes from `-g` or the `GATEWAY_HOST` environment
  variable.
- The API key comes from `-k`, the `API_KEY` environment variable,
  or an `api_key.conf` file with a `[KEY]` section containing `api_key = ...`.
  - See also https://docs.airlock.com/gateway/latest/index/rest-api/config-rest-api.html#create-a-token
- Mutating commands **save** the configuration by default and only
  **activate** when `--activate` is given; `-y` skips confirmation prompts.
- Ctrl-C terminates the REST session cleanly before exiting.
- `--debug` prints every HTTP request and response to stderr, with the API
  key redacted.

## Running the commands

Every command is run with `go run` from the repository root. `-h` prints the
available flags. Commands with several operations take the operation as a
subcommand first, and `-h` goes after it:

```sh
go run ./examples/headerrewrite -h
go run ./examples/iplists update -h
```

Typical invocations:

```sh
go run ./examples/denyrulegroup enable -g gateway.example.com -group SQLI_PARAM_VALUE -k KEY -y --activate
go run ./examples/headerrewrite -g gateway.example.com -m '^intranet$' -host backend.internal.example.com -k KEY -y --activate
go run ./examples/iplists update -g gateway.example.com -i 3 --blacklist --mapping-regex '^cust' -k KEY
```

Each command's `main.go` starts with a doc comment describing its flags and
usage examples.

## Checking responses

The examples fold the two failure modes of a `*WithResponse` call — a
transport error and an unexpected HTTP status — into one check with the
`gateway.Expect*` helpers (`ExpectOK` for GET/PATCH, `ExpectCreated` for
POST, `ExpectNoContent` for Connect/Unconnect and Delete):

```go
res, err := gateway.ExpectOK(gw.GetMappingsWithResponse(ctx, nil))
if err != nil { /* transport error or non-200 status */ }
```

After a successful `Expect*`, the matching `JSON2xx` field is populated.
Its optional fields are pointers; each example carries a small generic
`deref` helper that turns nil-checking chains into one expression:

```go
name := deref(deref(m.Attributes).Name) // "" if either pointer is nil
```

On failure the `Expect*` error includes the gateway's JSON:API error
details.

## Building request bodies

Request bodies can be built two ways, and the examples use both.

**Typed structs** (used by most commands). Construct the generated
`...JSONRequestBody` value and pass it to the operation:

```go
gw.UpdateMappingWithResponse(ctx, id, gateway.UpdateMappingJSONRequestBody{ /* ... */ })
```

The compiler checks field names and types, and your editor autocompletes the
schema. Optional fields are pointers, so wrap scalars with `gateway.Ptr(...)`.

**Raw JSON** (used by `headerrewrite/`). Every operation has a
`...WithBodyWithResponse(ctx, ..., contentType, body io.Reader)` variant, so
you can send a JSON document directly. When the body is fixed, pass it as a
string:

```go
body := `{"data": {"type": "ip-address-list", "id": "3"}}`
gw.ConnectIPAddressBlacklistsToMappingWithBodyWithResponse(
    ctx, mappingID, "application/json", strings.NewReader(body))
```

When values are dynamic, keep the document as a template and inject each
value through `json.Marshal` (or `%q` for a numeric id) — Go's `%q`/`%v`
quoting is not the same as JSON escaping:

```go
name, _ := json.Marshal(userProvidedName)
body := fmt.Sprintf(`{"data": {"type": "mapping", "id": %q,
    "attributes": {"name": %s}}}`, mappingID, name)
```

### Simplifying with constructors

The typed request bodies carry the JSON:API envelope — a fixed `type` string
plus the `Data`/`Attributes` wrapper — which is pure ceremony repeating at
every write call site:

```go
created, err := gateway.ExpectCreated(gw.CreateCustomDenyRuleGroupWithResponse(ctx,
    gateway.CreateCustomDenyRuleGroupJSONRequestBody{
        Data: gateway.ResourceObjectCustomDenyRuleGroupDto{
            Type:       "custom-deny-rule-group",
            Attributes: &gateway.CustomDenyRuleGroupDto{Name: groupName},
        },
    }))
```

A small hand-written constructor hides the envelope once, and the call site
keeps only what varies:

```go
created, err := gateway.ExpectCreated(gw.CreateCustomDenyRuleGroupWithResponse(ctx,
    exutil.NewCustomDenyRuleGroup(groupName)))
```

`customdenyrule/` uses this pattern; the constructors live in
`internal/exutil/builders.go`. The library deliberately ships no per-resource
constructors — the API surface is far too large to cover, and which arguments
deserve promotion to parameters depends on your use case — so copy the
pattern into your own tooling for the resources you actually create. For
linking resources, the library-provided `gateway.ToOne`/`gateway.ToMany`
helpers do the same job for all Connect/Unconnect relationship bodies.
