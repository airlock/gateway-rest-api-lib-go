package exutil

import "github.com/airlock/gateway-rest-api-lib-go/gateway"

// Request-body constructors that hide the JSON:API envelope (resource type
// string + attributes wrapper) of write operations. They demonstrate the
// "generated core + hand-written constructor" pattern: the library ships the
// generated *JSONRequestBody types as-is, and consumers who find the envelope
// noisy can wrap the resources they actually create, like this.

// NewCustomDenyRule builds the request body for CreateCustomDenyRule.
func NewCustomDenyRule(name string, restrictions *gateway.CustomDenyRuleRestrictionsDto) gateway.CreateCustomDenyRuleJSONRequestBody {
	return gateway.CreateCustomDenyRuleJSONRequestBody{
		Data: gateway.ResourceObjectCustomDenyRuleDto{
			Type:       "custom-deny-rule",
			Attributes: &gateway.CustomDenyRuleDto{Name: name, Restrictions: restrictions},
		},
	}
}

// NewCustomDenyRuleGroup builds the request body for CreateCustomDenyRuleGroup.
func NewCustomDenyRuleGroup(name string) gateway.CreateCustomDenyRuleGroupJSONRequestBody {
	return gateway.CreateCustomDenyRuleGroupJSONRequestBody{
		Data: gateway.ResourceObjectCustomDenyRuleGroupDto{
			Type:       "custom-deny-rule-group",
			Attributes: &gateway.CustomDenyRuleGroupDto{Name: name},
		},
	}
}
