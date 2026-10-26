# gateway-rest-api-lib-go

Go library for easier use of Airlock Gateway's Configuration REST API.

The current version of this library has been developed and tested for hosts
running Airlock Gateway 8.6; see
[Gateway compatibility](#gateway-compatibility) for other releases.

The full API reference is published on
[pkg.go.dev](https://pkg.go.dev/github.com/airlock/gateway-rest-api-lib-go/gateway).

Several example commands are provided under [`examples/`](examples/) to be
used as a starting point for developing your own tooling.

## Installation

```sh
go get github.com/airlock/gateway-rest-api-lib-go@latest
```

Requires Go 1.27 or newer.

## Getting started

The Configuration REST API is session-based and stateful. You open a session,
load a configuration into it, read or change resources, and finally save or
activate. Nothing you change takes effect on the gateway until you save or
activate.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	// Freshly installed management interfaces commonly use self-signed certificates; 
	// drop this option if yours has a proper CA-signed cert or after replacing the self-signed cert with one.
	gw, err := gateway.New(os.Getenv("GATEWAY_HOST"), os.Getenv("API_KEY"),
		gateway.WithInsecureSkipVerify())
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	if err := gw.Connect(ctx); err != nil { // opens the REST session
		log.Fatal(err)
	}
	defer gw.Close(ctx) // always terminate the session

	// Load the currently active configuration into the session.
	if _, err := gateway.ExpectNoContent(gw.LoadConfigurationActiveWithResponse(ctx)); err != nil {
		log.Fatal(err)
	}

	res, err := gateway.ExpectOK(gw.GetVirtualHostsWithResponse(ctx, nil))
	if err != nil {
		log.Fatal(err)
	}
	for _, vh := range *res.JSON200.Data {
		fmt.Println(*vh.Attributes.Name)
	}
}
```

Always pair `Connect` with `Close`: an abandoned session stays open on the
gateway until it times out server-side.

Every endpoint of the Configuration REST API is available as a method on
`*gateway.Client`, with typed request and response bodies.

To find the method for a given endpoint, search the generated client
`gateway/gateway.gen.go` for the path. Every method's doc comment names
the HTTP method, the path and the OpenAPI operationId it implements.
Method names are the operationId from `spec/active.json` with an upper-case
first letter; the `...WithResponse` variants return typed response bodies.

### Reading

Collection endpoints accept the API's filter syntax:

```go
res, err := gateway.ExpectOK(gw.GetVirtualHostsWithResponse(ctx,
	&gateway.GetVirtualHostsParams{
		Filter: gateway.Ptr([]string{"hostName==www.example.com"}),
	}))
```

Optional fields are pointers throughout, so guard them when reading;
`gateway.Ptr` is the inverse, for building requests.

### Changing and saving

Optional fields being pointers means a PATCH body carries only the attributes
you actually set — everything else is omitted rather than overwritten:

```go
_, err := gateway.ExpectOK(gw.UpdateVirtualHostWithResponse(ctx, 42,
	gateway.UpdateVirtualHostJSONRequestBody{
		Data: gateway.ResourceObjectVirtualHostDto{
			// Type and ID must match the resource being patched. If either is
			// missing, the gateway answers 200 OK but applies nothing.
			Type:       "virtual-host",
			ID:         gateway.Ptr("42"),
			Attributes: &gateway.VirtualHostDto{ShowMaintenancePage: gateway.Ptr(true)},
		},
	}))
```

Always set `Type` and `ID` on the outer resource object, even though `ID` is
optional in the generated type. A PATCH without them is accepted with
200 OK but silently changes nothing, so the omission does not surface as an
error.

The change lives in the session until you persist it — either save it as a new
configuration, or activate it to make it live:

```go
// Save without activating:
_, err = gateway.ExpectOK(gw.SaveConfigurationWithResponse(ctx,
	gateway.SaveConfigurationJSONRequestBody{Comment: gateway.Ptr("maintenance page")}))

// ...or activate, which applies the configuration to the running gateway:
_, err = gateway.ExpectOK(gw.ActivateConfigurationWithResponse(ctx,
	gateway.ActivationRequestDto{Comment: gateway.Ptr("enable maintenance page")}))
```

Relationships between resources are linked with `gateway.ToOne` and
`gateway.ToMany`:

```go
_, err := gateway.ExpectNoContent(gw.ConnectMappingsToVirtualHostWithResponse(
	ctx, vhostID, gateway.ToMany("mapping", "7", "8")))
```

Binary endpoints expose the raw body — `ExportConfigurationCurrentWithResponse`
returns the configuration zip in `res.Body`.

The JSON:API envelope (`Data`/`Type`/`Attributes`) seen in the PATCH above can
be hidden behind small constructors for the resources you create; see
[Simplifying with constructors](examples/README.md#simplifying-with-constructors) for the
pattern.

## Error handling

Two things can go wrong independently in a call: the transport can fail, and
the gateway can answer with an unexpected HTTP status — which the generated
code treats as data, not as an error. The `Expect*` helpers fold both into a
single check:

```go
res, err := gateway.ExpectOK(gw.GetVirtualHostWithResponse(ctx, 999))
if err != nil {
	var apiErr *gateway.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
		// no virtual host with that id
	}
	log.Fatal(err)
}
// res.JSON200 is populated
```

Use `ExpectOK` for GET and PATCH, `ExpectCreated` for POST, and
`ExpectNoContent` for DELETE, Connect/Unconnect and the Load operations. This
is a rule of thumb with exceptions (save and activate return 200, for
example); when in doubt, the response struct only has a `JSON200` or
`JSON201` field for the status the API actually declares. On a status of 400
or above the returned error is a `*gateway.APIError` carrying the gateway's
JSON:API error details.

If you would rather inspect the status yourself, the per-code typed documents
are populated too (`res.JSON404`, `res.JSON400`, ...), and
`gateway.CheckStatus(status, body)` converts a status into an `*APIError`
without asserting a particular expected code.

## Configuration

`gateway.New` takes the host, the API key, and any number of options:

| Option | Purpose |
|--------|---------|
| `WithPort(int)` | Non-default management port (default 443). |
| `WithTimeout(d)` | Per-request timeout (default 30s). |
| `WithInsecureSkipVerify()` | Skip TLS certificate verification. |
| `WithCustomHTTPClient(*http.Client)` | Full control over TLS, proxies and transport. |
| `WithLogger(*slog.Logger)` | Where non-fatal warnings go (default `slog.Default()`). |
| `WithoutVersionCheck()` | Skip the gateway-version check performed by `Connect`. |

`WithTimeout` and `WithInsecureSkipVerify` cannot be combined with
`WithCustomHTTPClient` — configure those on the custom client instead. In
production, prefer a custom client with a proper `RootCAs` pool over
`WithInsecureSkipVerify`.

A `Client` is safe for concurrent use once `Connect` has returned; `Connect`
and `Close` themselves are not safe to call concurrently with other methods.

## Gateway compatibility

This library is generated from the OpenAPI specification of **one** gateway
release — currently 8.6.0, recorded in code as `gateway.GeneratedFromVersion`.
Releases are versioned by the library's own Go API, following ordinary
semantic versioning.

It works against other releases too, for the part of the API those releases
implement: unknown JSON fields are ignored when reading, and a write body only
carries the attributes you set explicitly, so nothing newer reaches the wire
unless you ask for it. Calling an endpoint or setting an attribute that your
gateway does not have is rejected by the gateway, not caught at compile time.

If you want a client that matches your gateway exactly, regenerate it from a
vendored spec under `spec/versions/`, or from your own gateway's
specification — supported for gateway 8.5.1 and newer, see
[gateway/README.md](gateway/README.md).

`Connect` reads the gateway's actual version from the status endpoint and

- logs a warning on minor version skew,
- returns a `*gateway.VersionSkewError` on major version skew,
- carries on if the gateway does not report a version (for example when the
  API key lacks the permission for it).

`WithoutVersionCheck()` opts out entirely. After `Connect`, `gw.Version()`
reports the version detected on the gateway.

## Examples

Runnable example commands live in [`examples/`](examples/): maintenance pages,
deny rule groups and exceptions, IP address lists, certificate rotation,
header rewrites, and a guided tutorial through a full configuration
round-trip. Each is intended solely as an example and is provided "as is"
without any warranty or official support; see
[examples/README.md](examples/README.md).

```sh
GATEWAY_HOST=gateway.example.com API_KEY=eyJ... go run ./examples
```

## Development

Everything about regenerating the client from an OpenAPI specification,
adopting a new gateway release, and the test strategy is documented in
[gateway/README.md](gateway/README.md).

## License

MIT — see [LICENSE](LICENSE).
