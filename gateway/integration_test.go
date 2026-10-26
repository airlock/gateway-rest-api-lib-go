//go:build integration

// Integration tests against a REAL Airlock Gateway. Excluded from normal
// builds; run with:
//
//	TEST_HOST=gateway.example.com:8443 \
//	TEST_API_KEY=eyJ... \
//	make integration
//
// Optional:
//
//	TEST_EXPECT_VERSION=8.6   fail unless the gateway reports this
//	                                  major.minor
//
// The write test creates a mapping from a system template, SAVES the
// configuration and deletes that saved configuration again. It never
// activates, so the running configuration is untouched. To run only the read
// test:
//
//	TEST_HOST=... TEST_API_KEY=... make integration RUN=TestIntegrationRead
package gateway_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func integrationClient(t *testing.T) *Client {
	t.Helper()
	host := os.Getenv("TEST_HOST")
	key := os.Getenv("TEST_API_KEY")
	if host == "" || key == "" {
		t.Skip("TEST_HOST / TEST_API_KEY not set")
	}
	gw, err := New(host, key, WithInsecureSkipVerify(), WithTimeout(60*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return gw
}

func TestIntegrationRead(t *testing.T) {
	gw := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Connect performs the version check; major skew fails here by design.
	if err := gw.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer gw.Close(context.Background())

	t.Logf("gateway version: %s (client generated from %s)",
		gw.Version(), GeneratedFromVersion)

	if want := os.Getenv("TEST_EXPECT_VERSION"); want != "" {
		if !strings.HasPrefix(gw.Version(), want) {
			t.Fatalf("gateway reports %s, expected %s.x — wrong host configured?",
				gw.Version(), want)
		}
	}

	if res, err := gw.LoadConfigurationActiveWithResponse(ctx); err != nil {
		t.Fatalf("load-active: %v", err)
	} else if err := CheckStatus(res.StatusCode(), res.Body); err != nil {
		t.Fatalf("load-active: %v", err)
	}

	// Read every resource collection the automation depends on.
	vhs, err := gw.GetVirtualHostsWithResponse(ctx, nil)
	if err != nil {
		t.Fatalf("virtual-hosts: %v", err)
	}
	if err := CheckStatus(vhs.StatusCode(), vhs.Body); err != nil {
		t.Fatalf("virtual-hosts: %v", err)
	}
	maps, err := gw.GetMappingsWithResponse(ctx, nil)
	if err != nil {
		t.Fatalf("mappings: %v", err)
	}
	if err := CheckStatus(maps.StatusCode(), maps.Body); err != nil {
		t.Fatalf("mappings: %v", err)
	}
	certs, err := gw.GetSSLCertificatesWithResponse(ctx)
	if err != nil {
		t.Fatalf("ssl-certificates: %v", err)
	}
	if err := CheckStatus(certs.StatusCode(), certs.Body); err != nil {
		t.Fatalf("ssl-certificates: %v", err)
	}
	nVH, nMap, nCert := 0, 0, 0
	if vhs.JSON200 != nil && vhs.JSON200.Data != nil {
		nVH = len(*vhs.JSON200.Data)
	}
	if maps.JSON200 != nil && maps.JSON200.Data != nil {
		nMap = len(*maps.JSON200.Data)
	}
	if certs.JSON200 != nil && certs.JSON200.Data != nil {
		nCert = len(*certs.JSON200.Data)
	}
	t.Logf("read %d virtual hosts, %d mappings, %d ssl-certificates", nVH, nMap, nCert)
}

func TestIntegrationWrite(t *testing.T) {
	gw := integrationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := gw.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer gw.Close(context.Background())

	if res, err := gw.LoadConfigurationActiveWithResponse(ctx); err != nil {
		t.Fatalf("load-active: %v", err)
	} else if err := CheckStatus(res.StatusCode(), res.Body); err != nil {
		t.Fatalf("load-active: %v", err)
	}

	// From a system template, because the gateway fills most mapping defaults
	// in from one.
	tmplID := anyMappingTemplateID(t, ctx, gw)
	created, err := ExpectCreated(gw.CreateMappingFromTemplateWithResponse(ctx,
		CreateMappingFromTemplateJSONRequestBody{
			Data: ResourceObjectCreateMappingFromTemplateDto{
				Type:       "create-mapping-from-template",
				Attributes: &CreateMappingFromTemplateDto{ID: tmplID},
			},
		}))
	if err != nil {
		t.Fatalf("create mapping from template: %v", err)
	}
	mappingID, err := strconv.ParseInt(*created.JSON201.Data.ID, 10, 64)
	if err != nil {
		t.Fatalf("created mapping id: %v", err)
	}
	name := fmt.Sprintf("integration-test-%d", time.Now().Unix())
	if _, err := ExpectOK(gw.UpdateMappingWithResponse(ctx, mappingID,
		UpdateMappingJSONRequestBody{
			Data: ResourceObjectMappingDto{
				Type: "mapping", ID: created.JSON201.Data.ID,
				Attributes: &MappingDto{Name: Ptr(name)},
			},
		})); err != nil {
		t.Fatalf("rename mapping: %v", err)
	}
	t.Logf("created mapping %q (id %d) in the session", name, mappingID)

	// ...and SAVE (never activate): proves writing works against this gateway
	// version without touching the running configuration.
	comment := fmt.Sprintf("integration test %s — safe to delete", time.Now().Format(time.RFC3339))
	saved, err := ExpectOK(gw.SaveConfigurationWithResponse(ctx, SaveConfigurationJSONRequestBody{
		Comment: Ptr(comment),
	}))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	t.Logf("write verified; saved configuration comment: %q", comment)

	// Clean up: delete the saved test configuration again so repeated runs
	// leave nothing behind. The comment above stays the safety net for a
	// test that dies between save and delete.
	if saved.JSON200 == nil || saved.JSON200.Data == nil || saved.JSON200.Data.ID == nil {
		t.Fatal("save response carries no configuration id")
	}
	id, err := strconv.ParseInt(*saved.JSON200.Data.ID, 10, 64)
	if err != nil {
		t.Fatalf("saved configuration id: %v", err)
	}
	if _, err := ExpectNoContent(gw.DeleteConfigurationWithResponse(ctx, id)); err != nil {
		t.Errorf("delete saved test configuration %d: %v", id, err)
	}
}

// anyMappingTemplateID returns the ID of the first mapping template offered by
// the gateway. Names differ between releases, and the smoke test only needs a
// template that yields a valid mapping.
func anyMappingTemplateID(t *testing.T, ctx context.Context, gw *Client) string {
	t.Helper()
	tmpls, err := ExpectOK(gw.GetSystemTemplatesWithResponse(ctx, nil))
	if err != nil {
		t.Fatalf("get system templates: %v", err)
	}
	if tmpls.JSON200 == nil || tmpls.JSON200.Data == nil || len(*tmpls.JSON200.Data) == 0 {
		t.Fatal("gateway offers no mapping templates")
	}
	for _, tmpl := range *tmpls.JSON200.Data {
		if tmpl.ID == nil {
			continue
		}
		name := "?"
		if tmpl.Attributes != nil && tmpl.Attributes.Name != nil {
			name = *tmpl.Attributes.Name
		}
		t.Logf("using mapping template %q (id %s)", name, *tmpl.ID)
		return *tmpl.ID
	}
	t.Fatal("no mapping template carries an id")
	return ""
}
