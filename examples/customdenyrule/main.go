/*
Command customdenyrule creates a custom deny rule with a single
restriction and adds it to a custom deny rule group (creating the group
if it does not exist).

Usage example — deny all non-GET requests, in a group "CRUD restrictions":

	go run ./examples/customdenyrule -g gateway.example.com -k KEY \
	    -deny-rule-name "Deny non-GET" \
	    -pattern-type httpMethodPattern -pattern '^GET$' -inverted \
	    -custom-group-name "CRUD restrictions" \
	    -y -c "Add custom deny rule for non-GET requests" --activate

By default the change is saved; --activate activates it.
*/
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	cf := exutil.RegisterCommonFlags(flag.CommandLine, "Add custom deny rule")
	ruleName := flag.String("deny-rule-name", "", "name for the custom deny rule (required)")
	patternType := flag.String("pattern-type", "", "restriction type: httpMethodPattern|pathPattern|parameterNamePattern|parameterValuePattern|contentTypePattern|httpHeaderNamePattern|httpHeaderValuePattern (required)")
	pattern := flag.String("pattern", "", "regex pattern for the restriction (required)")
	patternName := flag.String("pattern-name", "", "display name for the restriction (generated if empty)")
	caseIgnored := flag.Bool("case-ignored", false, "set caseIgnored to true")
	inverted := flag.Bool("inverted", false, "set inverted to true")
	multipleRegex := flag.Bool("multiple-regex", false, "set multipleSingleLineRegex to true")
	groupName := flag.String("custom-group-name", "", "custom deny rule group name; created if it does not exist (required)")
	flag.Parse()
	if *ruleName == "" || *patternType == "" || *pattern == "" || *groupName == "" {
		log.Fatal("required: -deny-rule-name, -pattern-type, -pattern, -custom-group-name")
	}
	if *patternName == "" {
		*patternName = fmt.Sprintf("%s: %s", *patternType, *pattern)
	}
	p := &gateway.NamedTogglePatternTypeDto{
		Name:                    patternName,
		Pattern:                 *pattern,
		Enabled:                 gateway.Ptr(true),
		CaseIgnored:             caseIgnored,
		Inverted:                inverted,
		MultipleSingleLineRegex: multipleRegex,
	}
	restrictions := newRestrictions(*patternType, p)

	s := cf.Setup()
	defer s.Close()

	ruleID := createRule(s, *ruleName, restrictions)
	groupID := findOrCreateGroup(s, *groupName)
	connectRuleToGroup(s, ruleID, groupID)
	fmt.Printf("Custom deny rule %q added to group %q\n", *ruleName, *groupName)

	cf.Finish(s)
}

// newRestrictions returns restrictions with p in the field selected by patternType
func newRestrictions(patternType string, p *gateway.NamedTogglePatternTypeDto) *gateway.CustomDenyRuleRestrictionsDto {
	r := &gateway.CustomDenyRuleRestrictionsDto{}
	switch patternType {
	case "httpMethodPattern":
		r.HTTPMethodPattern = p
	case "pathPattern":
		r.PathPattern = p
	case "parameterNamePattern":
		r.ParameterNamePattern = p
	case "parameterValuePattern":
		r.ParameterValuePattern = p
	case "contentTypePattern":
		r.ContentTypePattern = p
	case "httpHeaderNamePattern":
		r.HTTPHeaderNamePattern = p
	case "httpHeaderValuePattern":
		r.HTTPHeaderValuePattern = p
	default:
		log.Fatalf("unknown pattern type %q", patternType)
	}
	return r
}

// createRule creates the custom deny rule and returns its ID
func createRule(s *exutil.Session, name string, restrictions *gateway.CustomDenyRuleRestrictionsDto) int64 {
	rule, err := gateway.ExpectCreated(s.GW.CreateCustomDenyRuleWithResponse(s.Ctx, exutil.NewCustomDenyRule(name, restrictions)))
	if err != nil {
		s.Fatal("create custom deny rule: %v", err)
	}
	id := exutil.ResourceID(rule.JSON201.Data.ID)
	if id < 0 {
		s.Fatal("unexpected non-numeric rule id %q", deref(rule.JSON201.Data.ID))
	}
	fmt.Printf("Created custom deny rule %q (ID %d)\n", name, id)
	return id
}

// findOrCreateGroup returns the ID of the custom deny rule group with the given name,
// creating it if needed
func findOrCreateGroup(s *exutil.Session, name string) string {
	groups, err := gateway.ExpectOK(s.GW.GetCustomDenyRuleGroupsWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("get custom deny rule groups: %v", err)
	}
	for _, g := range deref(groups.JSON200.Data) {
		if deref(g.Attributes).Name == name && g.ID != nil {
			fmt.Printf("Using existing custom deny rule group %q (ID %s)\n", name, *g.ID)
			return *g.ID
		}
	}
	created, err := gateway.ExpectCreated(s.GW.CreateCustomDenyRuleGroupWithResponse(s.Ctx, exutil.NewCustomDenyRuleGroup(name)))
	if err != nil {
		s.Fatal("create custom deny rule group: %v", err)
	}
	id := deref(created.JSON201.Data.ID)
	fmt.Printf("Created custom deny rule group %q (ID %s)\n", name, id)
	return id
}

// connectRuleToGroup adds the rule to the group
func connectRuleToGroup(s *exutil.Session, ruleID int64, groupID string) {
	if _, err := gateway.ExpectNoContent(s.GW.ConnectCustomDenyRuleGroupToCustomDenyRuleWithResponse(s.Ctx, ruleID,
		gateway.ToOne("custom-deny-rule-group", groupID))); err != nil {
		s.Fatal("connect rule to group: %v", err)
	}
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
