/*
Command setlogonly updates log-only mode on deny rule groups
(selected by a name regex) for all
mappings selected by a name regex, via:

	PATCH /configuration/mappings/{mappingId}/deny-rule-groups/{groupShortName}

Usage examples:

	enable log-only for all groups on all mappings starting with "cust":
	  go run ./examples/setlogonly -g gateway.example.com --mapping-regex '^cust' --group-regex '.*' -k KEY --activate
	disable log-only for one group:
	  go run ./examples/setlogonly -g gateway.example.com --mapping-regex '^cust' --group-regex 'SQLI_PARAM_VALUE' --disable -k KEY

By default changes are saved; --activate activates after confirmation.
*/
package main

import (
	"flag"
	"fmt"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	cf := exutil.RegisterCommonFlags(flag.CommandLine, "Script: update log-only on deny rule groups")
	mappingRegex := flag.String("mapping-regex", "", "regex to select mappings by name (required)")
	groupRegex := flag.String("group-regex", "", "regex to select deny rule groups by ID, e.g. SQLI_PARAM_VALUE (required)")
	disable := flag.Bool("disable", false, "disable log-only mode (default: enable)")
	flag.Parse()
	mre := exutil.MustRegexp(flag.CommandLine, "mapping-regex", *mappingRegex)
	gre := exutil.MustRegexp(flag.CommandLine, "group-regex", *groupRegex)

	s := cf.Setup()
	defer s.Close()

	mappings := s.Mappings(mre)
	if len(mappings) == 0 {
		s.Fatal("no mappings selected")
	}
	groups := s.DenyRuleGroups(gre)
	if len(groups) == 0 {
		s.Fatal("no deny rule groups selected")
	}

	logOnly := !*disable
	for _, m := range mappings {
		for _, g := range groups {
			setLogOnly(s, m, g, logOnly)
		}
	}

	cf.Finish(s)
}

// setLogOnly sets log-only mode of the deny rule group on the mapping
func setLogOnly(s *exutil.Session, m gateway.ResourceObjectMappingDto, group string, logOnly bool) {
	body := gateway.UpdateMappingDenyRuleGroupJSONRequestBody{
		Data: gateway.ResourceObjectMappingDenyRuleGroupDto{
			Type:       "mapping-deny-rule-group",
			Attributes: &gateway.MappingDenyRuleGroupDto{LogOnly: gateway.Ptr(logOnly)},
		},
	}
	name := exutil.MappingName(m)
	if _, err := gateway.ExpectOK(s.GW.UpdateMappingDenyRuleGroupWithResponse(s.Ctx, exutil.MappingID(m), group, body)); err != nil {
		fmt.Printf("Failed: mapping %q, group %s: %v\n", name, group, err)
		return
	}
	fmt.Printf("Set logOnly=%v: mapping %q, group %s\n", logOnly, name, group)
}
