package gateway

import "fmt"

// APIResponse is satisfied by every generated *WithResponse result type,
// letting the Expect helpers work uniformly across all operations.
type APIResponse interface {
	StatusCode() int
	Status() string
	Bytes() []byte
}

// Expect folds the two failure modes of a generated *WithResponse call — a
// transport error and an unexpected HTTP status — into a single error check.
// On status >= 400 the error is an *APIError, carrying the JSON:API errors
// array when the response has one.
//
// ExpectOK, ExpectCreated and ExpectNoContent take (res, err) directly, so
// they compose with the call:
//
//	res, err := gateway.ExpectOK(gw.GetVirtualHostsWithResponse(ctx, nil))
//	if err != nil { ... }
//	vhs := res.JSON200
func Expect[T APIResponse](res T, err error, want int) (T, error) {
	switch {
	case err != nil:
		return res, err // transport error; res may be nil
	case res.StatusCode() == want:
		return res, nil
	case res.StatusCode() >= 400:
		return res, CheckStatus(res.StatusCode(), res.Bytes())
	default:
		return res, fmt.Errorf("airlock: expected HTTP %d, got %d (%s)", want, res.StatusCode(), res.Status())
	}
}

// ExpectOK asserts HTTP 200 — the status returned by reads (GET) and updates
// (PATCH) on this API.
func ExpectOK[T APIResponse](res T, err error) (T, error) { return Expect(res, err, 200) }

// ExpectCreated asserts HTTP 201 — the status returned when a resource is
// created (POST).
func ExpectCreated[T APIResponse](res T, err error) (T, error) { return Expect(res, err, 201) }

// ExpectNoContent asserts HTTP 204 — the status returned by relationship
// (Connect/Unconnect) and Delete operations, which have no response body.
func ExpectNoContent[T APIResponse](res T, err error) (T, error) { return Expect(res, err, 204) }

// Ptr returns a pointer to v, for the optional fields of generated types.
func Ptr[T any](v T) *T { return &v }

// ToOne builds the request body for any Connect*/Unconnect* operation that
// links a single related resource, e.g. ConnectSSLCertificateToVirtualHost.
// typ is the JSON:API resource type of the target ("ssl-certificate",
// "mapping", …); id is its resource id.
func ToOne(typ, id string) ResourceRelationshipDocument {
	return ResourceRelationshipDocument{
		Data: RelationshipResourceObject{Type: typ, ID: id},
	}
}

// ToMany builds the request body for any Connect*/Unconnect* operation that
// links a collection, e.g. ConnectMappingsToVirtualHost. All ids share the
// resource type typ.
func ToMany(typ string, ids ...string) CollectionRelationshipDocument {
	data := make([]RelationshipResourceObject, len(ids))
	for i, id := range ids {
		data[i] = RelationshipResourceObject{Type: typ, ID: id}
	}
	return CollectionRelationshipDocument{Data: data}
}
