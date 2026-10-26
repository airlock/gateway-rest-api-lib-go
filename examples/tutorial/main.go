/*
Command tutorial is just for explanatory purposes and shows how to work
with the client: it saves a configuration after adding a virtual host, a backend
group and 10 mappings, another one after connecting the VH and the BEG
to one of those mappings, and a last one after deleting everything it
added. It NEVER ACTIVATES any changes — the running configuration of
your Airlock Gateway remains unchanged.

Usage:

	go run ./examples/tutorial -g gateway.example.com -k KEY
*/
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	cf := exutil.RegisterCommonFlags(flag.CommandLine, "Tutorial run")
	flag.Parse()

	s := cf.Setup()
	defer s.Close()

	backupConfig(s, "config.zip")

	// Create the test resources: 10 mappings, a backend group, and a virtual host.
	templateID := systemTemplateID(s, "New_Mapping")
	mappingIDs := createTestMappings(s, templateID, 10)
	begID := createBackendGroup(s)
	vhID := createVirtualHost(s)
	s.SaveConfig("Save before connecting mapping", true)

	// The virtual host was created without a certificate, so validation
	// must fail; observe the error, then connect a certificate to fix it.
	connectCertificate(s, vhID)

	// Connect the first created mapping to the virtual host and the BEG.
	connectMapping(s, mappingIDs[0], vhID, begID)
	s.SaveConfig("Save after connecting mappings", true)

	// Clean up everything this script created.
	deleteCreated(s, mappingIDs, begID, vhID)
	s.SaveConfig("Save after deleting mappings, virtual host and backend group", true)

	restoreBackup(s, "config.zip")
}

// backupConfig exports the current configuration and writes it to path.
func backupConfig(s *exutil.Session, path string) {
	backup, err := gateway.ExpectOK(s.GW.ExportConfigurationCurrentWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("export configuration: %v", err)
	}
	if err := os.WriteFile(path, backup.Body, 0o600); err != nil {
		s.Fatal("writing %s: %v", path, err)
	}
	log.Printf("backup of the current configuration written to %s (%d bytes)", path, len(backup.Body))
}

// systemTemplateID returns the ID of the system template with the given name
// ("New_Mapping" is the default template for plain new mappings).
func systemTemplateID(s *exutil.Session, name string) string {
	tmpls, err := gateway.ExpectOK(s.GW.GetSystemTemplatesWithResponse(s.Ctx, nil))
	if err != nil {
		s.Fatal("get system templates: %v", err)
	}
	for _, t := range deref(tmpls.JSON200.Data) {
		if deref(deref(t.Attributes).Name) == name {
			return deref(t.ID)
		}
	}
	s.Fatal("system template %q not found", name)
	return "" // unreachable, Fatal exits
}

// createTestMappings creates n mappings from the template, renames them to
// ABCtest0..ABCtest<n-1>, and returns their IDs.
func createTestMappings(s *exutil.Session, templateID string, n int) []int64 {
	var ids []int64
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("ABCtest%d", i)
		created, err := gateway.ExpectCreated(s.GW.CreateMappingFromTemplateWithResponse(s.Ctx, gateway.CreateMappingFromTemplateJSONRequestBody{
			Data: gateway.ResourceObjectCreateMappingFromTemplateDto{
				Type:       "create-mapping-from-template",
				Attributes: &gateway.CreateMappingFromTemplateDto{ID: templateID},
			},
		}))
		if err != nil {
			s.Fatal("create mapping: %v", err)
		}
		id := exutil.ResourceID(created.JSON201.Data.ID)
		ids = append(ids, id)

		if _, err := gateway.ExpectOK(s.GW.UpdateMappingWithResponse(s.Ctx, id, gateway.UpdateMappingJSONRequestBody{
			Data: gateway.ResourceObjectMappingDto{
				Type: "mapping", ID: created.JSON201.Data.ID,
				Attributes: &gateway.MappingDto{Name: gateway.Ptr(name)},
			},
		})); err != nil {
			s.Fatal("rename mapping: %v", err)
		}
		log.Printf("created mapping %q (id %d)", name, id)
	}
	return ids
}

// createBackendGroup creates the test backend group ABCtest_beg with one
// plain-HTTP host on port 80 and returns its ID.
func createBackendGroup(s *exutil.Session) int64 {
	beg, err := gateway.ExpectCreated(s.GW.CreateBackEndGroupWithResponse(s.Ctx, gateway.CreateBackEndGroupJSONRequestBody{
		Data: gateway.ResourceObjectBackendGroupDto{
			Type: "back-end-group",
			Attributes: &gateway.BackendGroupDto{
				Name: gateway.Ptr("ABCtest_beg"),
				BackendHosts: gateway.Ptr([]gateway.BackendHostDto{{
					HostName: gateway.Ptr("ABCtest_be"),
					Port:     gateway.Ptr(int32(80)),
					Protocol: gateway.Ptr(gateway.BackendHostDtoProtocol("HTTP")),
				}}),
			},
		},
	}))
	if err != nil {
		s.Fatal("create backend group: %v", err)
	}
	id := exutil.ResourceID(beg.JSON201.Data.ID)
	log.Printf("created backend group ABCtest_beg (id %d)", id)
	return id
}

// createVirtualHost adds the virtual host ABCtest_VH WITHOUT an SSL/TLS
// certificate, so that the validation error can be observed and fixed later.
// (Since Gateway 8.5, certificates are attached via the ssl-certificate
// relationship or ACME services.)
func createVirtualHost(s *exutil.Session) int64 {
	vh, err := gateway.ExpectCreated(s.GW.CreateVirtualHostWithResponse(s.Ctx, gateway.CreateVirtualHostJSONRequestBody{
		Data: gateway.ResourceObjectVirtualHostDto{
			Type: "virtual-host",
			Attributes: &gateway.VirtualHostDto{
				Name:     gateway.Ptr("ABCtest_VH"),
				HostName: gateway.Ptr("abctest.example.com"),
				NetworkInterface: &gateway.VirtualHostNetworkInterfaceDto{
					IPV4Address:                  gateway.Ptr("172.18.60.215/24"),
					ExternalLogicalInterfaceName: gateway.Ptr("INTERNAL"),
				},
			},
		},
	}))
	if err != nil {
		s.Fatal("create virtual host: %v", err)
	}
	id := exutil.ResourceID(vh.JSON201.Data.ID)
	log.Printf("created virtual host ABCtest_VH (id %d)", id)
	return id
}

// connectCertificate shows the validation error caused by the
// certificate-less virtual host and fixes it by connecting the first
// ssl-certificate available on the gateway.
func connectCertificate(s *exutil.Session, vhID int64) {
	errs := validationErrors(s)
	if len(errs) == 0 {
		fmt.Println("Something is wrong, this config should not be valid.")
		return
	}
	fmt.Println("Validation Error: " + errs[0])

	certs, err := gateway.ExpectOK(s.GW.GetSSLCertificatesWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("get ssl-certificates: %v", err)
	}
	certList := deref(certs.JSON200.Data)
	if len(certList) == 0 {
		log.Println("no ssl-certificate on the gateway to connect; leaving the validation error in place")
		return
	}
	certID := deref(certList[0].ID)
	if _, err := gateway.ExpectNoContent(s.GW.ConnectSSLCertificateToVirtualHostWithResponse(s.Ctx, vhID,
		gateway.ConnectSSLCertificateToVirtualHostJSONRequestBody{
			Data: gateway.RelationshipResourceObject{Type: "ssl-certificate", ID: certID},
		})); err != nil {
		s.Fatal("connect certificate: %v", err)
	}
	if errs := validationErrors(s); len(errs) == 0 {
		fmt.Println("Validation successful")
	} else {
		fmt.Println(errs[0])
	}
}

// connectMapping connects the virtual host and the backend group to the
// mapping, completing the VH -> mapping -> BEG chain.
func connectMapping(s *exutil.Session, mappingID, vhID, begID int64) {
	if _, err := gateway.ExpectNoContent(s.GW.ConnectVirtualHostsToMappingWithResponse(s.Ctx, mappingID,
		gateway.ConnectVirtualHostsToMappingJSONRequestBody{
			Data: []gateway.RelationshipResourceObject{{Type: "virtual-host", ID: fmt.Sprint(vhID)}},
		})); err != nil {
		s.Fatal("connect virtual host to mapping: %v", err)
	}
	if _, err := gateway.ExpectNoContent(s.GW.ConnectBackEndGroupsToMappingWithResponse(s.Ctx, mappingID,
		gateway.ConnectBackEndGroupsToMappingJSONRequestBody{
			Data: []gateway.RelationshipResourceObject{{Type: "back-end-group", ID: fmt.Sprint(begID)}},
		})); err != nil {
		s.Fatal("connect backend group to mapping: %v", err)
	}
}

// deleteCreated deletes the mappings, the backend group, and the virtual
// host this script created.
func deleteCreated(s *exutil.Session, mappingIDs []int64, begID, vhID int64) {
	for _, id := range mappingIDs {
		if _, err := gateway.ExpectNoContent(s.GW.DeleteMappingWithResponse(s.Ctx, id)); err != nil {
			s.Fatal("delete mapping %d: %v", id, err)
		}
	}
	if _, err := gateway.ExpectNoContent(s.GW.DeleteBackEndGroupWithResponse(s.Ctx, begID)); err != nil {
		s.Fatal("delete backend group: %v", err)
	}
	if _, err := gateway.ExpectNoContent(s.GW.DeleteVirtualHostWithResponse(s.Ctx, vhID)); err != nil {
		s.Fatal("delete virtual host: %v", err)
	}
}

// restoreBackup imports the configuration file at path into the session.
// Here this doesn't change anything as nothing was activated, but in general
// this is how you restore the backup stored at the beginning.
func restoreBackup(s *exutil.Session, path string) {
	zipBytes, err := os.ReadFile(path)
	if err != nil {
		s.Fatal("reading %s: %v", path, err)
	}
	if _, err := gateway.ExpectOK(s.GW.ImportConfigurationWithBodyWithResponse(s.Ctx, nil, "application/zip", bytes.NewReader(zipBytes))); err != nil {
		s.Fatal("import configuration: %v", err)
	}
	log.Println("backup imported into the session; script done (nothing was activated)")
}

// validationErrors returns the detail strings of ERROR-severity validator
// messages for the currently loaded configuration.
func validationErrors(s *exutil.Session) []string {
	res, err := gateway.ExpectOK(s.GW.GetValidatorMessagesWithResponse(s.Ctx, &gateway.GetValidatorMessagesParams{
		Filter: gateway.Ptr([]string{"meta.severity==ERROR"}),
	}))
	if err != nil {
		s.Fatal("validate: %v", err)
	}
	var out []string
	for _, m := range deref(res.JSON200.Data) {
		if detail := deref(deref(m.Attributes).Detail); detail != "" {
			out = append(out, detail)
		}
	}
	return out
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
