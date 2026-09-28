package forage

// Sensitivity is declared by the caller; Forage does not inspect prompts to infer it.
type Sensitivity string

const (
	SensitivityUnknown    Sensitivity = "unknown"
	SensitivityPublic     Sensitivity = "public"
	SensitivityRepository Sensitivity = "repository"
	SensitivitySensitive  Sensitivity = "sensitive"
)

// Need describes the constraints an execution route must satisfy.
type Need struct {
	ZeroCost              bool
	Sensitivity           Sensitivity
	AllowMayTrain         bool
	AllowUnknownPolicy    bool
	RequireLocal          bool
	RequireChat           bool
	RequireJSONSchema     bool
	RequireUsage          bool
	RequireReasoningUsage bool
	ContextTokens         int
	PinnedModel           string
}

// Rejection explains why one route was excluded by pure policy.
type Rejection struct {
	Route  string
	Reason string
}

// Decision is the pure policy result. Eligible routes remain advisory until
// Dispatcher performs its immediate pre-call revalidation.
type Decision struct {
	Eligible   []Route
	Rejections []Rejection
}

// Evaluate returns ordered eligible routes and the reasons every other route was rejected.
func Evaluate(routes []Route, need Need) Decision {
	decision := Decision{}
	for _, route := range routes {
		if reason := eligible(route, need); reason != "" {
			decision.Rejections = append(decision.Rejections, Rejection{Route: route.Name, Reason: reason})
			continue
		}
		decision.Eligible = append(decision.Eligible, route)
	}
	return decision
}

func eligible(route Route, need Need) string {
	if need.ZeroCost && route.CostClass != CostFree {
		return "cost is not explicitly free"
	}
	if need.PinnedModel != "" && route.Model != need.PinnedModel {
		return "model does not match pinned model"
	}
	if need.RequireLocal && route.DataPolicy != DataPolicyLocal {
		return "route is not local"
	}
	if !dataAllowed(route.DataPolicy, need) {
		return "data policy is not permitted"
	}
	if need.RequireChat && !route.Capabilities.Chat {
		return "chat is not supported"
	}
	if need.RequireJSONSchema && !route.Capabilities.JSONSchema {
		return "JSON Schema is not supported"
	}
	if need.RequireUsage && !route.Capabilities.Usage {
		return "usage is not supported"
	}
	if need.RequireReasoningUsage && !route.Capabilities.ReasoningUsage {
		return "reasoning usage is not supported"
	}
	if need.ContextTokens < 0 || route.Capabilities.ContextTokens < need.ContextTokens {
		return "context capacity is insufficient"
	}
	return ""
}

func dataAllowed(policy DataPolicy, need Need) bool {
	if policy == DataPolicyLocal {
		return true
	}
	switch need.Sensitivity {
	case SensitivityPublic:
		return true
	case SensitivityRepository, SensitivitySensitive:
		if policy == DataPolicyNoTrain {
			return true
		}
		return (policy == DataPolicyMayTrain && need.AllowMayTrain) || (policy == DataPolicyUnknown && need.AllowUnknownPolicy)
	default:
		return false
	}
}
