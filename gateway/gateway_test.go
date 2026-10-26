package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/airlock/gateway-rest-api-lib-go/gateway"
)

// newTestClient points a Client at a httptest TLS server.
func newTestClient(t *testing.T, handler http.Handler, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	gw, err := New(u.Host, "test-key", append([]Option{WithCustomHTTPClient(srv.Client())}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gw
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(body))
}

// handleSessionCreate answers session/create with a session cookie.
func handleSessionCreate(mux *http.ServeMux) {
	mux.HandleFunc("POST /airlock/rest/session/create", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "v", Path: "/"})
		writeJSON(w, `{}`)
	})
}

// handleSessionTerminate answers session/terminate and reports whether it was called.
func handleSessionTerminate(mux *http.ServeMux) *atomic.Bool {
	var called atomic.Bool
	mux.HandleFunc("POST /airlock/rest/session/terminate", func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		writeJSON(w, `{}`)
	})
	return &called
}

// wantErrContaining fails unless err is non-nil and mentions every want.
func wantErrContaining(t *testing.T, err error, want ...string) {
	t.Helper()
	if err == nil {
		t.Error("expected an error")
		return
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
}

func TestNewRejectsConflictingClientOptions(t *testing.T) {
	if _, err := New("gw", "key", WithCustomHTTPClient(&http.Client{}), WithTimeout(time.Second)); err == nil {
		t.Error("WithCustomHTTPClient + WithTimeout: expected error")
	}
	if _, err := New("gw", "key", WithCustomHTTPClient(&http.Client{}), WithInsecureSkipVerify()); err == nil {
		t.Error("WithCustomHTTPClient + WithInsecureSkipVerify: expected error")
	}
	if _, err := New("gw", "key", WithCustomHTTPClient(&http.Client{}), WithPort(8443)); err != nil {
		t.Errorf("WithCustomHTTPClient + WithPort must be allowed: %v", err)
	}
}

func TestConnectAuthErrorContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /airlock/rest/session/create", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403) // empty body, like a real gateway on a bad API key
	})
	gw := newTestClient(t, mux)

	err := gw.Connect(context.Background())
	wantErrContaining(t, err, "create session", "check the API key")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 {
		t.Errorf("want wrapped *APIError with 403, got %T: %v", err, err)
	}
}

func TestNewHostPortHandling(t *testing.T) {
	if _, err := New("gw:8443", "key", WithPort(9443)); err == nil {
		t.Error("host with port + WithPort: expected error")
	}
	if _, err := New("gw:abc", "key"); err == nil {
		t.Error("non-numeric port in host: expected error")
	}
	for _, host := range []string{"gw", "gw:8443", "2001:db8::1", "[2001:db8::1]", "[2001:db8::1]:8443"} {
		if _, err := New(host, "key"); err != nil {
			t.Errorf("New(%q): %v", host, err)
		}
	}
}

// A host carrying a scheme must fail at New, naming what to pass instead.
func TestNewRejectsSchemeInHost(t *testing.T) {
	tests := []struct{ host, want string }{
		{"https://gw.example.com:8443", `"gw.example.com:8443"`},
		{"https://gw.example.com", `"gw.example.com"`},
		{"http://gw.example.com", `"gw.example.com"`},
		{"https://gw.example.com/airlock/rest", `"gw.example.com"`},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			_, err := New(tc.host, "key")
			wantErrContaining(t, err, tc.want)
		})
	}
}

func TestSessionFlowAndGeneratedGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /airlock/rest/session/create", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "abc123", Path: "/"})
		writeJSON(w, `{"meta":{"type":"jsonapi.metadata.document"}}`)
	})
	mux.HandleFunc("GET /airlock/rest/configuration/virtual-hosts", func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie("JSESSIONID"); err != nil || ck.Value != "abc123" {
			t.Errorf("JSESSIONID cookie not replayed (%v, %v)", ck, err)
		}
		if got := r.URL.Query()["filter"]; len(got) != 1 || got[0] != "name==vh1" {
			t.Errorf("filter query = %v, want [name==vh1]", got)
		}
		writeJSON(w, `{"data":[{"type":"virtual-host","id":"7",
			"attributes":{"name":"vh1","hostName":"vh1.example.com","keepAliveTimeout":10}}]}`)
	})

	gw := newTestClient(t, mux)
	ctx := context.Background()

	if err := gw.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	res, err := ExpectOK(gw.GetVirtualHostsWithResponse(ctx, &GetVirtualHostsParams{
		Filter: Ptr([]string{"name==vh1"}),
	}))
	if err != nil {
		t.Fatalf("GetVirtualHosts: %v", err)
	}
	if res.JSON200 == nil || res.JSON200.Data == nil || len(*res.JSON200.Data) != 1 {
		t.Fatalf("unexpected response: %+v", res.JSON200)
	}
	vh := (*res.JSON200.Data)[0]
	if *vh.ID != "7" || *vh.Attributes.Name != "vh1" || *vh.Attributes.KeepAliveTimeout != 10 {
		t.Errorf("unexpected virtual host: %+v", vh)
	}
}

func TestPatchBodyOmitsUnsetFields(t *testing.T) {
	var gotBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /airlock/rest/configuration/virtual-hosts/42", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		gotBody, _ = io.ReadAll(r.Body)
		writeJSON(w, `{"data":{"type":"virtual-host","id":"42","attributes":{"name":"renamed"}}}`)
	})

	gw := newTestClient(t, mux)
	res, err := ExpectOK(gw.UpdateVirtualHostWithResponse(context.Background(), 42,
		UpdateVirtualHostJSONRequestBody{
			Data: ResourceObjectVirtualHostDto{
				Type: "virtual-host",
				ID:   Ptr("42"),
				Attributes: &VirtualHostDto{
					Name:                Ptr("renamed"),
					ShowMaintenancePage: Ptr(true),
				},
			},
		}))
	if err != nil {
		t.Fatalf("UpdateVirtualHost: %v", err)
	}
	if res.JSON200 == nil || *res.JSON200.Data.Attributes.Name != "renamed" {
		t.Errorf("unexpected response: %+v", res.JSON200)
	}

	var sent map[string]any
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	attrs := sent["data"].(map[string]any)["attributes"].(map[string]any)
	if len(attrs) != 2 { // omitempty must drop all unset optional fields
		t.Errorf("expected exactly 2 attributes in PATCH body, got: %v", attrs)
	}
}

func TestBinaryExport(t *testing.T) {
	zip := []byte{0x50, 0x4b, 0x03, 0x04, 0xff}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /airlock/rest/configuration/configurations/export", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zip)
	})

	gw := newTestClient(t, mux)
	res, err := ExpectOK(gw.ExportConfigurationCurrentWithResponse(context.Background()))
	if err != nil {
		t.Fatalf("ExportConfigurationCurrent: %v", err)
	}
	if string(res.Body) != string(zip) {
		t.Errorf("binary payload mismatch: %v", res.Body)
	}
}

func TestAPIErrorDecoding(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"meta":{"type":"jsonapi.metadata.document"},"errors":[{"code":"ENTITY_NOT_FOUND"}]}`))
	})

	gw := newTestClient(t, mux)
	res, err := gw.GetVirtualHostWithResponse(context.Background(), 999)
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	err = CheckStatus(res.StatusCode(), res.Body)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 404 || len(apiErr.Errors) != 1 || *apiErr.Errors[0].Code != "ENTITY_NOT_FOUND" {
		t.Errorf("unexpected APIError: %+v", apiErr)
	}
	// Typed error documents are also decoded by the generated code:
	if res.JSON404 == nil || res.JSON404.Errors == nil || *(*res.JSON404.Errors)[0].Code != "ENTITY_NOT_FOUND" {
		t.Errorf("JSON404 not populated: %+v", res.JSON404)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{"status only",
			&APIError{StatusCode: 404},
			"airlock: HTTP 404"},

		{"code and detail",
			&APIError{StatusCode: 400, Errors: []ErrorData{
				{Code: Ptr("UNKNOWN_ATTRIBUTE"), Detail: Ptr("attribute 'icap' not available")}}},
			"airlock: HTTP 400 (UNKNOWN_ATTRIBUTE: attribute 'icap' not available)"},

		{"detail only",
			&APIError{StatusCode: 422, Errors: []ErrorData{
				{Detail: Ptr("name must not be empty")}}},
			"airlock: HTTP 422 (name must not be empty)"},

		{"title when detail is absent",
			&APIError{StatusCode: 400, Errors: []ErrorData{
				{Title: Ptr("Validation failed")}}},
			"airlock: HTTP 400 (Validation failed)"},

		{"detail preferred over title",
			&APIError{StatusCode: 400, Errors: []ErrorData{
				{Title: Ptr("Validation failed"), Detail: Ptr("name must not be empty")}}},
			"airlock: HTTP 400 (name must not be empty)"},

		{"code with title",
			&APIError{StatusCode: 400, Errors: []ErrorData{
				{Code: Ptr("VALIDATION"), Title: Ptr("Validation failed")}}},
			"airlock: HTTP 400 (VALIDATION: Validation failed)"},

		{"several errors joined",
			&APIError{StatusCode: 400, Errors: []ErrorData{
				{Code: Ptr("A"), Detail: Ptr("first")},
				{Code: Ptr("B"), Detail: Ptr("second")}}},
			"airlock: HTTP 400 (A: first; B: second)"},

		{"empty error objects fall back to the body",
			&APIError{StatusCode: 502, Errors: []ErrorData{{}, {}},
				Body: "<html>\n  Bad Gateway\n</html>"},
			"airlock: HTTP 502 (<html> Bad Gateway </html>)"},

		{"non-JSON body with no errors array",
			&APIError{StatusCode: 502, Body: "Bad Gateway"},
			"airlock: HTTP 502 (Bad Gateway)"},

		{"nothing usable at all",
			&APIError{StatusCode: 500},
			"airlock: HTTP 500"},

		{"whitespace-only body is not usable",
			&APIError{StatusCode: 500, Body: "\n\t  \n"},
			"airlock: HTTP 500"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestAPIErrorTruncatesLongBody(t *testing.T) {
	err := &APIError{StatusCode: 502, Body: strings.Repeat("x", 5000)}
	msg := err.Error()
	if len(msg) > 300 {
		t.Errorf("message not truncated: %d chars", len(msg))
	}
	if !strings.HasSuffix(msg, "…)") {
		t.Errorf("truncation not marked: %q", msg[len(msg)-20:])
	}
}

func TestVersionSkewErrorMessage(t *testing.T) {
	err := &VersionSkewError{GatewayVersion: "9.0.0", ClientVersion: "8.6.0"}
	wantErrContaining(t, err, "9.0.0", "8.6.0", "WithoutVersionCheck")
}

// --- version guard ---------------------------------------------------------

// skewedVersion is GeneratedFromVersion shifted by the given major and minor deltas.
func skewedVersion(majorSkew, minorSkew int) string {
	var major, minor int
	fmt.Sscanf(GeneratedFromVersion, "%d.%d", &major, &minor)
	return fmt.Sprintf("%d.%d.0", major+majorSkew, minor+minorSkew)
}

func versionMux(version string) *http.ServeMux {
	mux := http.NewServeMux()
	handleSessionCreate(mux)
	mux.HandleFunc("GET /airlock/rest/system/status/node", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, `{"data":{"type":"node-status","attributes":{"hostName":"gw1","version":"`+version+`","failoverState":"standalone","countUserLogins":1}}}`)
	})
	return mux
}

func TestVersionGuardMatchAndMinorSkew(t *testing.T) {
	// exact match: no error, version exposed
	gw := newTestClient(t, versionMux(GeneratedFromVersion))
	if err := gw.Connect(context.Background()); err != nil {
		t.Fatalf("Connect (matching version): %v", err)
	}
	if gw.Version() != GeneratedFromVersion {
		t.Errorf("GatewayVersion = %q", gw.Version())
	}

	// minor skew: warning only, no error
	gw2 := newTestClient(t, versionMux(skewedVersion(0, 1)))
	if err := gw2.Connect(context.Background()); err != nil {
		t.Fatalf("Connect (minor skew): %v", err)
	}
}

func TestVersionGuardSuffixAndUnparseable(t *testing.T) {
	// release suffix ("8.6.0-r2"): same major.minor, must connect cleanly
	gw := newTestClient(t, versionMux(GeneratedFromVersion+"-r2"))
	if err := gw.Connect(context.Background()); err != nil {
		t.Fatalf("Connect (suffixed version): %v", err)
	}

	// unparseable version: warn and continue, never fail
	gw2 := newTestClient(t, versionMux("unparseable"))
	if err := gw2.Connect(context.Background()); err != nil {
		t.Fatalf("Connect (unparseable version): %v", err)
	}
	if gw2.Version() != "unparseable" {
		t.Errorf("GatewayVersion = %q, want the raw reported string", gw2.Version())
	}
}

func TestVersionGuardMajorMismatch(t *testing.T) {
	major := skewedVersion(1, 0)
	gw := newTestClient(t, versionMux(major))
	err := gw.Connect(context.Background())
	var skew *VersionSkewError
	if !errors.As(err, &skew) {
		t.Fatalf("want *VersionSkewError, got %T: %v", err, err)
	}
	if skew.GatewayVersion != major || skew.ClientVersion != GeneratedFromVersion {
		t.Errorf("unexpected skew error: %+v", skew)
	}

	// opting out must succeed against the same gateway
	gw2 := newTestClient(t, versionMux(major), WithoutVersionCheck())
	if err := gw2.Connect(context.Background()); err != nil {
		t.Fatalf("Connect with WithoutVersionCheck: %v", err)
	}
}

func TestConnectVersionSkewTerminatesSession(t *testing.T) {
	mux := versionMux(skewedVersion(1, 0))
	terminated := handleSessionTerminate(mux)

	gw := newTestClient(t, mux)
	err := gw.Connect(context.Background())
	var skew *VersionSkewError
	if !errors.As(err, &skew) {
		t.Fatalf("want *VersionSkewError, got %T: %v", err, err)
	}
	if !terminated.Load() {
		t.Error("Connect rejected the gateway on version skew but did not terminate the created session")
	}
}

func TestVersionGuardEndpointUnavailable(t *testing.T) {
	// status endpoint forbidden (restricted API key roles): warn, not fail
	mux := http.NewServeMux()
	handleSessionCreate(mux)
	mux.HandleFunc("GET /airlock/rest/system/status/node", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"errors":[{"code":"FORBIDDEN"}]}`))
	})
	gw := newTestClient(t, mux)
	if err := gw.Connect(context.Background()); err != nil {
		t.Fatalf("Connect must not fail when version is undeterminable: %v", err)
	}
	if gw.Version() != "" {
		t.Errorf("GatewayVersion should be empty, got %q", gw.Version())
	}
}

// WithLogger must receive the non-fatal warnings, such as minor version skew.
func TestWithLoggerReceivesSkewWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Same major, different minor: warns, does not fail.
	minor := skewedVersion(0, 1)
	gw := newTestClient(t, versionMux(minor), WithLogger(logger))
	if err := gw.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "gateway version differs") {
		t.Errorf("custom logger did not receive the skew warning; got %q", out)
	}
	if !strings.Contains(out, minor) {
		t.Errorf("warning does not name the gateway version; got %q", out)
	}
}

// --- close session ---------------------------------------------------------

// A deferred Close must still reach the gateway when the caller's context
// is already cancelled.
func TestCloseTerminatesSessionAfterContextCancel(t *testing.T) {
	mux := http.NewServeMux()
	handleSessionCreate(mux)
	terminated := handleSessionTerminate(mux)
	gw := newTestClient(t, mux)

	ctx, cancel := context.WithCancel(context.Background())
	if err := gw.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	cancel() // the caller's context dies before the deferred Close runs

	if err := gw.Close(ctx); err != nil {
		t.Errorf("Close after cancel: %v", err)
	}
	if !terminated.Load() {
		t.Error("session was not terminated on the gateway; the REST session leaked")
	}
}

// Close drops cancellation but not the context's values.
func TestCloseKeepsContextValues(t *testing.T) {
	type ctxKey struct{}
	var seen atomic.Value
	mux := http.NewServeMux()
	handleSessionTerminate(mux)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)

	// Read the value off the request Close actually sends.
	hc := srv.Client()
	base := hc.Transport // capture before wrapping; srv.Client() is a singleton
	hc.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if v, ok := req.Context().Value(ctxKey{}).(string); ok {
			seen.Store(v)
		}
		return base.RoundTrip(req)
	})
	gw, err := New(u.Host, "k", WithCustomHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "abc"))
	cancel()
	if err := gw.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := seen.Load(); got != "abc" {
		t.Errorf("context value = %v, want \"abc\" (values must survive WithoutCancel)", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
