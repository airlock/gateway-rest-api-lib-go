/*
Command denyruleexceptions adds, deletes, or lists exceptions on
BUILT-IN deny rule groups for mappings selected by a name regex.
(Custom deny rule groups use a different endpoint and are not
handled here.)

Usage examples:

	add an exception for the parameter "comment" to all groups on all MyBank mappings:
	  go run ./examples/denyruleexceptions add -g gateway.example.com -mapping-regex '^MyBank' -group-regex '.*' \
	      -parameter-name '^comment$' -i paymentComment1 -c test --activate -k KEY
	delete it again:
	  go run ./examples/denyruleexceptions delete -g gateway.example.com -mapping-regex '^MyBank' -group-regex '.*' \
	      -i paymentComment1 -c test --activate -k KEY
	list exceptions:
	  go run ./examples/denyruleexceptions list -g gateway.example.com -mapping-regex '^MyBank' -group-regex '.*' -k KEY
*/
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	op, fs := exutil.Subcommand("denyruleexceptions", "add", "delete", "list")
	cf := exutil.RegisterConnectFlags(fs)
	mappingRegex := fs.String("mapping-regex", "", "regex to select mappings (required)")
	groupRegex := fs.String("group-regex", "", "regex to select deny rule groups by ID, e.g. SQLI_PARAM_VALUE (required)")
	var identifier, parameterName, headerName string
	if op != "list" {
		cf.RegisterChangeFlags(fs, "Modify exceptions through REST API")
		fs.StringVar(&identifier, "i", "", "identifier (name) of the exception (required)")
	}
	if op == "add" {
		fs.StringVar(&parameterName, "parameter-name", "", "parameter name pattern for the exception")
		fs.StringVar(&headerName, "header-name", "", "header name pattern for the exception")
	}
	fs.Parse(os.Args[2:])
	mre := exutil.MustRegexp(fs, "mapping-regex", *mappingRegex)
	gre := exutil.MustRegexp(fs, "group-regex", *groupRegex)
	if op != "list" && identifier == "" {
		log.Fatal("-i <identifier> is required")
	}
	if op == "add" && parameterName == "" && headerName == "" {
		log.Fatal("add requires -parameter-name and/or -header-name")
	}

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

	changed := false
	for _, m := range mappings {
		for _, g := range groups {
			exceptions, ok := currentExceptions(s, m, g)
			if !ok {
				continue // group not applicable to this mapping
			}
			switch op {
			case "list":
				printExceptions(m, g, exceptions)
			case "add":
				exc := newException(identifier, parameterName, headerName)
				if addException(s, m, g, exceptions, exc) {
					changed = true
				}
			case "delete":
				if deleteException(s, m, g, exceptions, identifier) {
					changed = true
				}
			}
		}
	}

	if op == "list" {
		return
	}
	if !changed {
		log.Println("nothing changed; not saving")
		return
	}
	cf.Finish(s)
}

// currentExceptions returns the group's exceptions on the mapping; ok is
// false if the group is not applicable to the mapping
func currentExceptions(s *exutil.Session, m gateway.ResourceObjectMappingDto, g string) (exceptions []gateway.DenyRuleExceptionDto, ok bool) {
	cur, err := gateway.ExpectOK(s.GW.GetMappingDenyRuleGroupWithResponse(s.Ctx, exutil.MappingID(m), g))
	var apiErr *gateway.APIError
	if errors.As(err, &apiErr) {
		return nil, false
	}
	if err != nil {
		s.Fatal("get group %s on mapping %d: %v", g, exutil.MappingID(m), err)
	}
	return deref(deref(cur.JSON200.Data.Attributes).Exceptions), true
}

// printExceptions prints each exception as one JSON line
func printExceptions(m gateway.ResourceObjectMappingDto, g string, exceptions []gateway.DenyRuleExceptionDto) {
	for _, e := range exceptions {
		pretty, _ := json.Marshal(e)
		fmt.Printf("mapping %q, group %s: %s\n", exutil.MappingName(m), g, pretty)
	}
}

// newException builds an enabled exception named identifier from the non-empty patterns
func newException(identifier, parameterName, headerName string) gateway.DenyRuleExceptionDto {
	exc := gateway.DenyRuleExceptionDto{Enabled: gateway.Ptr(true)}
	if parameterName != "" {
		exc.ParameterNamePattern = &gateway.NamedTogglePatternTypeDto{
			Name: gateway.Ptr(identifier), Pattern: parameterName, Enabled: gateway.Ptr(true),
		}
	}
	if headerName != "" {
		exc.HeaderNamePattern = &gateway.NamedTogglePatternTypeDto{
			Name: gateway.Ptr(identifier), Pattern: headerName, Enabled: gateway.Ptr(true),
		}
	}
	return exc
}

// addException appends exc to the group's exceptions and reports whether the
// group was updated
func addException(s *exutil.Session, m gateway.ResourceObjectMappingDto, g string, exceptions []gateway.DenyRuleExceptionDto, exc gateway.DenyRuleExceptionDto) bool {
	if !setExceptions(s, m, g, append(exceptions, exc)) {
		return false
	}
	fmt.Printf("Added exception %q: mapping %q, group %s\n", exceptionName(exc), exutil.MappingName(m), g)
	return true
}

// deleteException removes the exceptions named identifier from the group and
// reports whether the group was updated
func deleteException(s *exutil.Session, m gateway.ResourceObjectMappingDto, g string, exceptions []gateway.DenyRuleExceptionDto, identifier string) bool {
	kept := exceptions[:0]
	for _, e := range exceptions {
		if exceptionName(e) != identifier {
			kept = append(kept, e)
		}
	}
	if len(kept) == len(exceptions) || !setExceptions(s, m, g, kept) {
		return false
	}
	fmt.Printf("Deleted exception %q: mapping %q, group %s\n", identifier, exutil.MappingName(m), g)
	return true
}

// setExceptions replaces the group's exceptions on the mapping
func setExceptions(s *exutil.Session, m gateway.ResourceObjectMappingDto, g string, exceptions []gateway.DenyRuleExceptionDto) bool {
	body := gateway.UpdateMappingDenyRuleGroupJSONRequestBody{
		Data: gateway.ResourceObjectMappingDenyRuleGroupDto{
			Type:       "mapping-deny-rule-group",
			Attributes: &gateway.MappingDenyRuleGroupDto{Exceptions: gateway.Ptr(exceptions)},
		},
	}
	if _, err := gateway.ExpectOK(s.GW.UpdateMappingDenyRuleGroupWithResponse(s.Ctx, exutil.MappingID(m), g, body)); err != nil {
		fmt.Printf("Failed: mapping %q, group %s: %v\n", exutil.MappingName(m), g, err)
		return false
	}
	return true
}

// exceptionName returns the identifier under which an exception was added
// (the Name of its first pattern).
func exceptionName(e gateway.DenyRuleExceptionDto) string {
	for _, p := range []*gateway.NamedTogglePatternTypeDto{
		e.ParameterNamePattern, e.HeaderNamePattern, e.ContentTypePattern,
	} {
		if name := deref(deref(p).Name); name != "" {
			return name
		}
	}
	return ""
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
