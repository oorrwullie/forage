// Package forage contains the provider-neutral route and policy core.
package forage

import (
	"fmt"
	"strings"
)

// CostClass describes whether a route can charge for inference.
type CostClass string

const (
	CostUnknown CostClass = "unknown"
	CostFree    CostClass = "free"
	CostPaid    CostClass = "paid"
)

// DataPolicy describes the route operator's declared treatment of submitted data.
type DataPolicy string

const (
	DataPolicyUnknown  DataPolicy = "unknown"
	DataPolicyLocal    DataPolicy = "local"
	DataPolicyNoTrain  DataPolicy = "no-train"
	DataPolicyMayTrain DataPolicy = "may-train"
)

// Capabilities declares behavior needed for a request to use a route.
type Capabilities struct {
	Chat           bool `json:"chat" yaml:"chat"`
	JSONSchema     bool `json:"json_schema" yaml:"json_schema"`
	Usage          bool `json:"usage" yaml:"usage"`
	ReasoningUsage bool `json:"reasoning_usage" yaml:"reasoning_usage"`
	ContextTokens  int  `json:"context_tokens" yaml:"context_tokens"`
}

// Route is one concrete execution path. The same model may exist on several routes.
type Route struct {
	Name         string       `json:"name" yaml:"name"`
	Provider     string       `json:"provider" yaml:"provider"`
	Model        string       `json:"model" yaml:"model"`
	Endpoint     string       `json:"endpoint" yaml:"endpoint"`
	CostClass    CostClass    `json:"cost" yaml:"cost"`
	DataPolicy   DataPolicy   `json:"data_policy" yaml:"data_policy"`
	Capabilities Capabilities `json:"capabilities" yaml:"capabilities"`
}

// Validate verifies a route's explicit configuration shape. Eligibility is
// intentionally evaluated separately for each request.
func (r Route) Validate() error {
	if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.Provider) == "" || strings.TrimSpace(r.Model) == "" || strings.TrimSpace(r.Endpoint) == "" {
		return fmt.Errorf("name, provider, model, and endpoint are required")
	}
	if !validCost(r.CostClass) {
		return fmt.Errorf("invalid cost class %q", r.CostClass)
	}
	if !validDataPolicy(r.DataPolicy) {
		return fmt.Errorf("invalid data policy %q", r.DataPolicy)
	}
	if r.Capabilities.ContextTokens < 0 {
		return fmt.Errorf("context tokens must not be negative")
	}
	return nil
}

func validCost(c CostClass) bool {
	return c == CostFree || c == CostPaid || c == CostUnknown
}

func validDataPolicy(p DataPolicy) bool {
	return p == DataPolicyLocal || p == DataPolicyNoTrain || p == DataPolicyMayTrain || p == DataPolicyUnknown
}
