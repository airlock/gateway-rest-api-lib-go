package gateway_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

// fakeResp is a minimal gateway.APIResponse.
type fakeResp struct {
	code int
	body []byte
}

func (f fakeResp) StatusCode() int { return f.code }
func (f fakeResp) Status() string  { return fmt.Sprintf("%d Testing", f.code) }
func (f fakeResp) Bytes() []byte   { return f.body }

func TestExpect(t *testing.T) {
	transport := errors.New("dial tcp: boom")

	t.Run("transport error wins over a matching status", func(t *testing.T) {
		_, err := gateway.Expect(fakeResp{code: 200}, transport, 200)

		if !errors.Is(err, transport) {
			t.Errorf("got %v, want the transport error", err)
		}
	})

	t.Run("matching status is not an error", func(t *testing.T) {
		_, err := gateway.Expect(fakeResp{code: 201}, nil, 201)

		if err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})

	t.Run("status 400 or above becomes an APIError", func(t *testing.T) {
		_, err := gateway.Expect(fakeResp{code: 409}, nil, 201)

		var apiErr *gateway.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 409 {
			t.Errorf("got %T %v, want *APIError{StatusCode:409}", err, err)
		}
	})

	t.Run("unexpected 2xx is a plain error", func(t *testing.T) {
		_, err := gateway.Expect(fakeResp{code: 200}, nil, 201)

		var apiErr *gateway.APIError
		if err == nil || errors.As(err, &apiErr) {
			t.Errorf("got %v, want a non-APIError error", err)
		}
	})
}

// A nil response pointer must pass through without a method call, which would panic.
func TestExpectNilPointerResponse(t *testing.T) {
	transport := errors.New("dial tcp: boom")
	res, err := gateway.Expect((*gateway.GetMappingsResponse)(nil), transport, 200)
	if !errors.Is(err, transport) {
		t.Errorf("got %v, want the transport error", err)
	}
	if res != nil {
		t.Errorf("res = %v, want nil", res)
	}
}

func TestExpectDecodesErrorBody(t *testing.T) {
	res := fakeResp{
		code: 400,
		body: []byte(`{"errors":[{"code":"UNKNOWN_ATTRIBUTE","detail":"attribute \"nope\""}]}`),
	}
	_, err := gateway.Expect(res, nil, 200)
	var apiErr *gateway.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if len(apiErr.Errors) != 1 || *apiErr.Errors[0].Code != "UNKNOWN_ATTRIBUTE" {
		t.Errorf("errors not decoded from body: %+v", apiErr)
	}
	if apiErr.Body == "" {
		t.Error("raw body not preserved")
	}
}

func TestStatusWrappers(t *testing.T) {
	cases := []struct {
		name     string
		fn       func(fakeResp, error) (fakeResp, error)
		expected int
	}{
		{"ExpectOK", gateway.ExpectOK[fakeResp], 200},
		{"ExpectCreated", gateway.ExpectCreated[fakeResp], 201},
		{"ExpectNoContent", gateway.ExpectNoContent[fakeResp], 204},
	}
	for _, c := range cases {
		if _, err := c.fn(fakeResp{code: c.expected}, nil); err != nil {
			t.Errorf("%s(%d): unexpected error %v", c.name, c.expected, err)
		}
		if _, err := c.fn(fakeResp{code: 299}, nil); err == nil {
			t.Errorf("%s: expected error on status 299", c.name)
		}
	}
}

func TestToOne(t *testing.T) {
	got := gateway.ToOne("ssl-certificate", "42")
	if got.Data.Type != "ssl-certificate" || got.Data.ID != "42" {
		t.Errorf("ToOne = %+v", got.Data)
	}
}

func TestToMany(t *testing.T) {
	got := gateway.ToMany("mapping", "1", "2", "3")
	if len(got.Data) != 3 {
		t.Fatalf("ToMany produced %d entries, want 3", len(got.Data))
	}
	for i, want := range []string{"1", "2", "3"} {
		if got.Data[i].Type != "mapping" || got.Data[i].ID != want {
			t.Errorf("entry %d = %+v, want {mapping %s}", i, got.Data[i], want)
		}
	}

	if empty := gateway.ToMany("mapping"); len(empty.Data) != 0 {
		t.Errorf("ToMany() with no ids = %+v", empty.Data)
	}
}
