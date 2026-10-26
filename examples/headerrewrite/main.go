/*
Command headerrewrite adds a custom rewrite rule for the Host request
header to mappings selected by name, and disables the gateway's built-in
default Host-header rewrite rule for those mappings.

It builds its request bodies as raw JSON documents rather than typed DTO
structs; see examples/README.md for that style and its trade-offs.

Usage:

	go run ./examples/headerrewrite -g gateway.example.com -k KEY \
	    -m '^intranet$' -host backend.internal.example.com \
	    -y -c "Rewrite Host header" --activate

By default the change is saved; --activate activates it.
*/
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	cf := exutil.RegisterCommonFlags(flag.CommandLine, "Rewrite Host header")
	mappingRegex := flag.String("m", "", "regex selecting mappings by name (required)")
	hostValue := flag.String("host", "", "value to set the Host header to (required)")
	actionName := flag.String("action-name", "Rewrite Host header", "name for the custom header action")
	flag.Parse()
	re := exutil.MustRegexp(flag.CommandLine, "m", *mappingRegex)
	if *hostValue == "" {
		log.Fatal("-host is required")
	}

	s := cf.Setup()
	defer s.Close()

	def := findDefaultHostAction(s)
	if def == nil {
		log.Println("no default Host-header rewrite rule found; will only add the custom rule")
	}
	mappings := s.Mappings(re)
	if len(mappings) == 0 {
		s.Fatal("no mappings match %q", *mappingRegex)
	}
	for _, m := range mappings {
		rewriteHost(s, m, *actionName, *hostValue, def)
	}

	cf.Finish(s)
}

// headerAction identifies a default header action by ID and JSON:API type
type headerAction struct {
	ID   int64
	Type string
}

// findDefaultHostAction returns the default Host-header rewrite rule, or nil
// if there is none
func findDefaultHostAction(s *exutil.Session) *headerAction {
	acts, err := gateway.ExpectOK(s.GW.GetHeaderActionsRequestWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("get default request header actions: %v", err)
	}
	for _, a := range deref(acts.JSON200.Data) {
		if a == nil || a.ID == nil {
			continue
		}
		if strings.EqualFold(deref(deref(a.Attributes).HeaderName), "Host") {
			if id, err := strconv.ParseInt(*a.ID, 10, 64); err == nil {
				return &headerAction{ID: id, Type: a.Type}
			}
		}
	}
	return nil
}

// rewriteHost adds the custom Host rewrite to the mapping and disables the
// default Host rewrite rule def, if any
func rewriteHost(s *exutil.Session, m gateway.ResourceObjectMappingDto, actionName, hostValue string, def *headerAction) {
	id, name := exutil.MappingID(m), exutil.MappingName(m)
	if err := addCustomHostRewrite(s, m, actionName, hostValue); err != nil {
		fmt.Printf("Failed to add custom Host rewrite on mapping %q: %v\n", name, err)
		return
	}
	fmt.Printf("Added custom Host rewrite %q on mapping %q (ID %d)\n", actionName, name, id)

	if def == nil {
		return
	}
	if err := disableDefaultHostRewrite(s, id, def); err != nil {
		fmt.Printf("Failed to disable default Host rewrite on mapping %q: %v\n", name, err)
		return
	}
	fmt.Printf("Disabled default Host rewrite on mapping %q (ID %d)\n", name, id)
}

// addCustomHostRewrite adds a custom action that rewrites the Host header on
// the mapping. Existing custom actions are preserved: a PATCH replaces the
// whole customActions list, so the current actions are re-sent with the new
// one appended.
func addCustomHostRewrite(s *exutil.Session, m gateway.ResourceObjectMappingDto, actionName, hostValue string) error {
	id := exutil.MappingID(m)
	actions := append(existingCustomActions(m), map[string]any{
		"name":           actionName,
		"actionType":     "ADD_OR_REPLACE_HEADER",
		"headerName":     "Host",
		"newHeaderValue": hostValue,
		"enabled":        true,
	})
	actionsJSON, err := json.Marshal(actions)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"data": {
    "type": "mapping",
    "id": %q,
    "attributes": {"application": {"request": {"header": {"customActions": %s}}}}
}}`, strconv.FormatInt(id, 10), actionsJSON)
	_, err = gateway.ExpectOK(s.GW.UpdateMappingWithBodyWithResponse(s.Ctx, id, "application/json", strings.NewReader(body)))
	return err
}

// disableDefaultHostRewrite sets the mapping's use of the default Host-header
// rewrite rule to DISABLED (the other states are ENABLED and INHERIT).
func disableDefaultHostRewrite(s *exutil.Session, mappingID int64, def *headerAction) error {
	typeJSON, _ := json.Marshal(def.Type)
	body := fmt.Sprintf(`{"data": {"type": %s, "id": %q, "attributes": {"active": "DISABLED"}}}`,
		typeJSON, strconv.FormatInt(def.ID, 10))
	_, err := gateway.ExpectOK(s.GW.UpdateMappingHeaderActionRequestWithBodyWithResponse(s.Ctx, mappingID, def.ID, "application/json", strings.NewReader(body)))
	return err
}

// existingCustomActions returns the mapping's current request-header custom
// actions, or nil if it has none.
func existingCustomActions(m gateway.ResourceObjectMappingDto) []any {
	var shape struct {
		Application struct {
			Request struct {
				Header struct {
					CustomActions []any `json:"customActions"`
				} `json:"header"`
			} `json:"request"`
		} `json:"application"`
	}
	raw, _ := json.Marshal(m.Attributes)
	_ = json.Unmarshal(raw, &shape)
	return shape.Application.Request.Header.CustomActions
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
