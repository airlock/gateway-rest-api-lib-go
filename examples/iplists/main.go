/*
Command iplists works with IP address lists.
It supports two operations:

	list:   list all IP address lists (IDs, names, IPs)
	update: assign an IP list to mappings selected by a name regex,
	        either as blacklist (relationship on the mapping) or as
	        whitelist (pathWhitelists entry in the mapping's ipRules)

Usage examples:

	go run ./examples/iplists list -g gateway.example.com -k KEY
	go run ./examples/iplists update -g gateway.example.com -i 3 --blacklist --mapping-regex '^cust' -y -c "Add blacklist" -k KEY
	go run ./examples/iplists update -g gateway.example.com -i 3 --whitelist --mapping-regex '^cust' --path-pattern '^/api/' -k KEY

By default, configuration changes are saved; add --activate to activate.
*/
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	op, fs := exutil.Subcommand("iplists", "list", "update")
	cf := exutil.RegisterConnectFlags(fs)
	if op == "list" {
		fs.Parse(os.Args[2:])
		s := cf.Setup()
		defer s.Close()
		listIPAddressLists(s)
		return
	}

	cf.RegisterChangeFlags(fs, "Script: update IP list relationships")
	ipListID := fs.Int64("i", 0, "IP address list ID (required)")
	blacklist := fs.Bool("blacklist", false, "assign the IP list as blacklist to the selected mappings")
	whitelist := fs.Bool("whitelist", false, "assign the IP list as whitelist entry on the selected mappings")
	mappingRegex := fs.String("mapping-regex", "", "regex to select mappings (required)")
	pathPattern := fs.String("path-pattern", "", "path pattern for the whitelist entry (required with --whitelist)")
	fs.Parse(os.Args[2:])
	if *ipListID == 0 || *blacklist == *whitelist {
		log.Fatal("-i <id> and exactly one of --blacklist / --whitelist are required")
	}
	if *whitelist && *pathPattern == "" {
		log.Fatal("--whitelist requires --path-pattern")
	}
	re := exutil.MustRegexp(fs, "mapping-regex", *mappingRegex)

	s := cf.Setup()
	defer s.Close()

	mappings := s.Mappings(re)
	if len(mappings) == 0 {
		s.Fatal("no mappings selected")
	}
	for _, m := range mappings {
		if *blacklist {
			addBlacklist(s, m, *ipListID)
		} else {
			addWhitelist(s, m, *ipListID, *pathPattern)
		}
	}

	cf.Finish(s)
}

// listIPAddressLists prints ID, name, and IPs of every IP address list.
func listIPAddressLists(s *exutil.Session) {
	res, err := gateway.ExpectOK(s.GW.GetIPAddressListsWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("get ip address lists: %v", err)
	}
	lists := deref(res.JSON200.Data)
	if len(lists) == 0 {
		fmt.Println("No IP address lists found.")
		return
	}
	for _, l := range lists {
		attrs := deref(l.Attributes)
		fmt.Printf("ID: %s, Name: %s, IPs: %v\n", deref(l.ID), deref(attrs.Name), deref(attrs.Ips))
	}
}

// addBlacklist adds the IP list to the mapping's blacklists
func addBlacklist(s *exutil.Session, m gateway.ResourceObjectMappingDto, ipListID int64) {
	body := gateway.ConnectIPAddressBlacklistsToMappingJSONRequestBody{
		Data: []gateway.RelationshipResourceObject{{Type: "ip-address-list", ID: fmt.Sprint(ipListID)}},
	}
	id, name := exutil.MappingID(m), exutil.MappingName(m)
	if _, err := gateway.ExpectNoContent(s.GW.ConnectIPAddressBlacklistsToMappingWithResponse(s.Ctx, id, body)); err != nil {
		fmt.Printf("Failed to update blacklist on mapping %q: %v\n", name, err)
		return
	}
	fmt.Printf("Blacklist updated on mapping %q (ID %d)\n", name, id)
}

// addWhitelist adds the IP list to the mapping's path whitelist entry for pathPattern
func addWhitelist(s *exutil.Session, m gateway.ResourceObjectMappingDto, ipListID int64, pathPattern string) {
	body := gateway.UpdateMappingJSONRequestBody{
		Data: gateway.ResourceObjectMappingDto{
			Type: "mapping", ID: m.ID,
			Attributes: &gateway.MappingDto{
				IPRules: &gateway.MappingIPRulesDto{
					IPAddressWhitelists: &gateway.IPAddressWhitelistsDto{
						PathWhitelists: gateway.Ptr(mergedWhitelists(m, pathPattern, ipListID)),
					},
				},
			},
		},
	}
	id, name := exutil.MappingID(m), exutil.MappingName(m)
	if _, err := gateway.ExpectOK(s.GW.UpdateMappingWithResponse(s.Ctx, id, body)); err != nil {
		fmt.Printf("Failed to update whitelist on mapping %q: %v\n", name, err)
		return
	}
	fmt.Printf("Whitelist entry for path %q updated on mapping %q (ID %d)\n", pathPattern, name, id)
}

// mergedWhitelists returns the mapping's ipRules path-whitelist entries with
// ipListID added to the entry for pathPattern, appending a new entry if none
// matches.
func mergedWhitelists(m gateway.ResourceObjectMappingDto, pathPattern string, ipListID int64) []gateway.PathIPAddressWhitelistDto {
	merged := deref(deref(deref(deref(m.Attributes).IPRules).IPAddressWhitelists).PathWhitelists)
	for i, e := range merged {
		if deref(deref(e.PathPattern).Pattern) == pathPattern {
			merged[i].IPAddressListIds = gateway.Ptr(appendUnique(deref(e.IPAddressListIds), ipListID))
			return merged
		}
	}
	return append(merged, gateway.PathIPAddressWhitelistDto{
		Enabled:          gateway.Ptr(true),
		IPAddressListIds: gateway.Ptr([]int64{ipListID}),
		PathPattern: &gateway.CaseSensitiveInvertablePatternTypeDto{
			Pattern: gateway.Ptr(pathPattern),
		},
	})
}

func appendUnique(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	return append(ids, id)
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
