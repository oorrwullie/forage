// Package forage contains the provider-neutral route and policy core.
package forage

import "fmt"

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
	Chat           bool `yaml:"chat"`
	JSONSchema     bool `yaml:"json_schema"`
	Usage          bool `yaml:"usage"`
	ReasoningUsage bool `yaml:"reasoning_usage"`
	ContextTokens  int  `yaml:"context_tokens"`
}

// Route is one concrete execution path. The same model may exist on several routes.
type Route struct {
	Name         string       `yaml:"name"`
	Provider     string       `yaml:"provider"`
	Model        string       `yaml:"model"`
	Endpoint     string       `yaml:"endpoint"`
	CostClass    CostClass    `yaml:"cost"`
	DataPolicy   DataPolicy   `yaml:"data_policy"`
	Capabilities Capabilities `yaml:"capabilities"`
}

// Validate verifies a route's explicit configuration shape. Eligibility is
// intentionally evaluated separately for each request.
func (r Route) Validate() error {
	if r.Name == "" || r.Provider == "" || r.Model == "" || r.Endpoint == "" {
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
