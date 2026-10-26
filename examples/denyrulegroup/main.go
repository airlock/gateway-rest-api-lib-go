/*
Command denyrulegroup enables or disables a deny rule group on ALL
mappings.

Usage example:

	go run ./examples/denyrulegroup enable -g gateway.example.com --group SQLI_PARAM_VALUE \
	    -k YOUR_API_KEY -y -c "Update deny rule group" --activate

By default, changes are saved (but not activated). To activate the
configuration, supply the --activate flag.
*/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	op, fs := exutil.Subcommand("denyrulegroup", "enable", "disable")
	cf := exutil.RegisterCommonFlags(fs, "Script: "+op+" deny rule group {group} for all mappings")
	group := fs.String("group", "", "deny rule group short name, e.g. SQLI_PARAM_VALUE (required)")
	fs.Parse(os.Args[2:])
	if *group == "" {
		fmt.Fprintln(os.Stderr, "-group is required")
		fs.Usage()
		os.Exit(2)
	}

	s := cf.Setup()
	defer s.Close()

	mappings := s.Mappings(nil)
	if len(mappings) == 0 {
		s.Fatal("no mappings found")
	}
	for _, m := range mappings {
		printGroupSettings(s, m, *group)
		setGroupEnabled(s, m, *group, op == "enable")
	}

	comment := strings.ReplaceAll(cf.Comment, "{group}", *group)
	s.ActivateOrSave(comment, cf.AssumeYes, cf.Activate)
}

// printGroupSettings prints the current settings of the deny rule group on the mapping
func printGroupSettings(s *exutil.Session, m gateway.ResourceObjectMappingDto, group string) {
	id := exutil.MappingID(m)
	cur, err := gateway.ExpectOK(s.GW.GetMappingDenyRuleGroupWithResponse(s.Ctx, id, group))
	if err != nil {
		s.Fatal("get deny rule group on mapping %d: %v", id, err)
	}
	fmt.Printf("Mapping ID %d: current settings:\n", id)
	pretty, _ := json.MarshalIndent(cur.JSON200.Data.Attributes, "", "    ")
	fmt.Println(string(pretty))
}

// setGroupEnabled enables or disables the deny rule group on the mapping
func setGroupEnabled(s *exutil.Session, m gateway.ResourceObjectMappingDto, group string, enable bool) {
	body := gateway.UpdateMappingDenyRuleGroupJSONRequestBody{
		Data: gateway.ResourceObjectMappingDenyRuleGroupDto{
			Type:       "mapping-deny-rule-group",
			Attributes: &gateway.MappingDenyRuleGroupDto{Enabled: gateway.Ptr(enable)},
		},
	}
	id, name := exutil.MappingID(m), exutil.MappingName(m)
	if _, err := gateway.ExpectOK(s.GW.UpdateMappingDenyRuleGroupWithResponse(s.Ctx, id, group, body)); err != nil {
		fmt.Printf("Failed to update mapping %q (ID %d): %v\n", name, id, err)
		return
	}
	fmt.Printf("Updated mapping %q, mapping ID: %d\n", name, id)
}
