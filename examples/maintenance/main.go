/*
Command maintenance manages the
maintenance page setting on Airlock mappings selected by a name regex.

	go run ./examples/maintenance show -g gateway.example.com --mapping-regex '^shop' -k KEY
	go run ./examples/maintenance enable -g gateway.example.com --mapping-regex '^shop' -k KEY
	go run ./examples/maintenance disable -g gateway.example.com --mapping-regex '^shop' -k KEY

By default, configuration changes are saved; add --activate to activate.
*/
package main

import (
	"fmt"
	"os"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	op, fs := exutil.Subcommand("maintenance", "show", "enable", "disable")
	cf := exutil.RegisterConnectFlags(fs)
	if op != "show" {
		cf.RegisterChangeFlags(fs, "Script: "+op+" maintenance page")
	}
	mappingRegex := fs.String("mapping-regex", "", "regular expression to select mappings (required)")
	fs.Parse(os.Args[2:])
	re := exutil.MustRegexp(fs, "mapping-regex", *mappingRegex)

	s := cf.Setup()
	defer s.Close()

	mappings := s.Mappings(re)
	if len(mappings) == 0 {
		s.Fatal("no mappings found matching the selector pattern")
	}

	if op == "show" {
		showMaintenancePage(mappings)
		return // read-only: nothing to save
	}
	for _, m := range mappings {
		setMaintenancePage(s, m, op == "enable")
	}

	printSummary(op, mappings)
	cf.Finish(s)
}

// showMaintenancePage prints the maintenance page status of each mapping
func showMaintenancePage(mappings []gateway.ResourceObjectMappingDto) {
	fmt.Println("Mapping Name, Maintenance Page Status")
	for _, m := range mappings {
		status := "N/A"
		if m.Attributes != nil && m.Attributes.EnableMaintenancePage != nil {
			status = fmt.Sprint(*m.Attributes.EnableMaintenancePage)
		}
		fmt.Printf("%s, %s\n", exutil.MappingName(m), status)
	}
}

// setMaintenancePage enables or disables the maintenance page of the mapping
func setMaintenancePage(s *exutil.Session, m gateway.ResourceObjectMappingDto, enable bool) {
	body := gateway.UpdateMappingJSONRequestBody{
		Data: gateway.ResourceObjectMappingDto{
			Type: "mapping", ID: m.ID,
			Attributes: &gateway.MappingDto{EnableMaintenancePage: gateway.Ptr(enable)},
		},
	}
	if _, err := gateway.ExpectOK(s.GW.UpdateMappingWithResponse(s.Ctx, exutil.MappingID(m), body)); err != nil {
		fmt.Printf("Failed to update mapping %q: %v\n", exutil.MappingName(m), err)
		return
	}
	fmt.Printf("Updated mapping %q successfully.\n", exutil.MappingName(m))
}

// printSummary lists the changed mappings before the save/activate prompt
func printSummary(action string, mappings []gateway.ResourceObjectMappingDto) {
	verb := "Enabled"
	if action == "disable" {
		verb = "Disabled"
	}
	fmt.Printf("\n%s maintenance page for the following mapping(s) in the loaded configuration (not yet saved):\n", verb)
	for _, m := range mappings {
		fmt.Printf("\t%s\n", exutil.MappingName(m))
	}
}
