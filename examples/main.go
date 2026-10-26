/*
Command example connects to an Airlock Gateway and lists virtual hosts
and mappings from the active configuration.

Usage:

	GATEWAY_HOST=gateway.example.com API_KEY=eyJ... go run ./examples
*/
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	host := os.Getenv("GATEWAY_HOST")
	apiKey := os.Getenv("API_KEY")
	if host == "" || apiKey == "" {
		log.Fatal("set GATEWAY_HOST and API_KEY")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Management interfaces commonly use self-signed certificates; drop
	// this option if yours has a proper CA-signed cert.
	gw, err := gateway.New(host, apiKey, gateway.WithInsecureSkipVerify())
	if err != nil {
		log.Fatal(err)
	}

	if err := gw.Connect(ctx); err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer gw.Close(context.Background())

	// The API is stateful: load a configuration before reading resources.
	// ExpectNoContent folds the transport error and the HTTP-status check
	// that would otherwise be two separate steps into a single call
	// (loading returns 204).
	if _, err := gateway.ExpectNoContent(gw.LoadConfigurationActiveWithResponse(ctx)); err != nil {
		log.Fatalf("load active config: %v", err)
	}

	vhs, err := gateway.ExpectOK(gw.GetVirtualHostsWithResponse(ctx, nil))
	if err != nil {
		log.Fatalf("virtual hosts: %v", err)
	}
	vhList := deref(vhs.JSON200.Data)
	fmt.Printf("Virtual hosts (%d):\n", len(vhList))
	for _, vh := range vhList {
		fmt.Printf("  [%s] %s\n", deref(vh.ID), deref(deref(vh.Attributes).Name))
	}

	mappings, err := gateway.ExpectOK(gw.GetMappingsWithResponse(ctx, nil))
	if err != nil {
		log.Fatalf("mappings: %v", err)
	}
	mappingList := deref(mappings.JSON200.Data)
	fmt.Printf("\nMappings (%d):\n", len(mappingList))
	for _, m := range mappingList {
		fmt.Printf("  [%s] %s\n", deref(m.ID), deref(deref(m.Attributes).Name))
	}

	// To change something: PATCH resources, then save or activate, e.g.
	//
	//	_, err = gw.ActivateConfigurationWithResponse(ctx, gateway.ActivationRequestDto{
	//		Comment: gateway.Ptr("automated change"),
	//	})
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
