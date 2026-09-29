package forage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/oorrwullie/forage"
)

func freeRoute() forage.Route {
	return forage.Route{
		Name: "free", Provider: "test", Model: "free-model", Endpoint: "http://example.test",
		CostClass: forage.CostFree, DataPolicy: forage.DataPolicyNoTrain,
		Capabilities: forage.Capabilities{Chat: true, JSONSchema: true, Usage: true, ReasoningUsage: true, ContextTokens: 4096},
	}
}

func zeroCostNeed() forage.Need {
	return forage.Need{ZeroCost: true, Sensitivity: forage.SensitivityPublic, RequireChat: true}
}

func TestEvaluateZeroCostAllowsOnlyExplicitFree(t *testing.T) {
	route := freeRoute()
	routes := []forage.Route{route, {Name: "paid", CostClass: forage.CostPaid, DataPolicy: forage.DataPolicyNoTrain, Capabilities: forage.Capabilities{Chat: true}}, {Name: "unknown", CostClass: forage.CostUnknown, DataPolicy: forage.DataPolicyNoTrain, Capabilities: forage.Capabilities{Chat: true}}}

	decision := forage.Evaluate(routes, zeroCostNeed())
	if len(decision.Eligible) != 1 || decision.Eligible[0].Name != route.Name {
		t.Fatalf("eligible = %#v, want only free route", decision.Eligible)
	}
	if len(decision.Rejections) != 2 {
		t.Fatalf("rejections = %#v, want two", decision.Rejections)
	}
}

func TestEvaluateRejectsTrainableDataForRepositoryUnlessPermitted(t *testing.T) {
	route := freeRoute()
	need := zeroCostNeed()
	need.Sensitivity = forage.SensitivityRepository
	for _, policy := range []forage.DataPolicy{forage.DataPolicyMayTrain, forage.DataPolicyUnknown} {
		route.DataPolicy = policy
		if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 0 {
			t.Fatalf("policy %q eligible = %#v, want rejection", policy, got.Eligible)
		}
	}
	route.DataPolicy = forage.DataPolicyMayTrain
	need.AllowMayTrain = true
	if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 1 {
		t.Fatalf("may-train eligible = %#v, want explicit permit", got.Eligible)
	}
	route.DataPolicy = forage.DataPolicyUnknown
	need.AllowUnknownPolicy = true
	if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 1 {
		t.Fatalf("unknown eligible = %#v, want explicit permit", got.Eligible)
	}
}

func TestEvaluateRejectsUnsetAndUnsupportedSensitivityIncludingLocal(t *testing.T) {
	route := freeRoute()
	route.DataPolicy = forage.DataPolicyLocal
	for _, sensitivity := range []forage.Sensitivity{"", forage.SensitivityUnknown, forage.Sensitivity("unsupported")} {
		need := zeroCostNeed()
		need.Sensitivity = sensitivity
		if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 0 {
			t.Fatalf("sensitivity %q eligible = %#v, want rejection", sensitivity, got.Eligible)
		}
	}
}

func TestEvaluateEnforcesContextAndPinnedModel(t *testing.T) {
	route := freeRoute()
	need := zeroCostNeed()
	need.ContextTokens = 8192
	if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 0 {
		t.Fatalf("eligible = %#v, want context rejection", got.Eligible)
	}
	need.ContextTokens = 1
	need.PinnedModel = "other-model"
	if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 0 {
		t.Fatalf("eligible = %#v, want pinned model rejection", got.Eligible)
	}
}

func TestEvaluatePinnedModelCannotBypassZeroCost(t *testing.T) {
	paid := freeRoute()
	paid.CostClass = forage.CostPaid
	paid.Model = "caller-selected-model"
	need := zeroCostNeed()
	need.PinnedModel = paid.Model
	if got := forage.Evaluate([]forage.Route{paid}, need); len(got.Eligible) != 0 {
		t.Fatalf("eligible = %#v, want paid pinned route rejected", got.Eligible)
	}
}

func TestAdapterErrorClassifications(t *testing.T) {
	if !(&forage.AdapterError{Kind: forage.ErrorProtocol}).FallbackEligible() {
		t.Fatal("Protocol must be fallback eligible")
	}
	mismatch := &forage.AdapterError{Kind: forage.ErrorEffectiveModelMismatch}
	if !mismatch.FallbackEligible() || !mismatch.RouteDisabling() {
		t.Fatalf("EffectiveModelMismatch classification = fallback:%t disabling:%t", mismatch.FallbackEligible(), mismatch.RouteDisabling())
	}
}

func TestDispatchRevalidatesRouteImmediatelyBeforeChat(t *testing.T) {
	route := freeRoute()
	resolver := &routeMap{routes: map[string]forage.Route{route.Name: route}}
	adapter := &recordingAdapter{}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}
	candidates := forage.Evaluate([]forage.Route{route}, zeroCostNeed()).Eligible

	mutated := route
	mutated.CostClass = forage.CostPaid
	resolver.routes[route.Name] = mutated

	_, err := dispatcher.Dispatch(context.Background(), candidates, forage.Request{Need: zeroCostNeed()})
	if !errors.Is(err, forage.ErrNoEligibleRoute) {
		t.Fatalf("Dispatch() error = %v, want ErrNoEligibleRoute", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("Adapter.Chat calls = %d, want 0 after route becomes paid", adapter.calls)
	}
}

func TestDispatchFallbackNeverCallsPaidRoute(t *testing.T) {
	free := freeRoute()
	paid := free
	paid.Name = "paid"
	paid.CostClass = forage.CostPaid
	resolver := &routeMap{routes: map[string]forage.Route{free.Name: free, paid.Name: paid}}
	adapter := &recordingAdapter{err: &forage.AdapterError{Kind: forage.ErrorProtocol}}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}

	_, err := dispatcher.Dispatch(context.Background(), []forage.Route{free, paid}, forage.Request{Need: zeroCostNeed()})
	var adapterErr *forage.AdapterError
	if !errors.As(err, &adapterErr) || adapterErr.Kind != forage.ErrorProtocol {
		t.Fatalf("Dispatch() error = %v, want Protocol adapter error", err)
	}
	if adapter.calls != 1 {
		t.Fatalf("Adapter.Chat calls = %d, want only free route call", adapter.calls)
	}
}

func TestDispatchDoesNotFallbackAfterEligibleRouteError(t *testing.T) {
	first := freeRoute()
	second := first
	second.Name = "second"
	resolver := &routeMap{routes: map[string]forage.Route{first.Name: first, second.Name: second}}
	wantErr := &forage.AdapterError{Kind: forage.ErrorProtocol}
	adapter := &recordingAdapter{err: wantErr}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}

	_, err := dispatcher.Dispatch(context.Background(), []forage.Route{first, second}, forage.Request{Need: zeroCostNeed()})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Dispatch() error = %v, want original adapter error", err)
	}
	if adapter.calls != 1 {
		t.Fatalf("Adapter.Chat calls = %d, want exactly one", adapter.calls)
	}
}

func TestDispatchRejectsUnsetAndUnsupportedSensitivityBeforeChat(t *testing.T) {
	route := freeRoute()
	route.DataPolicy = forage.DataPolicyLocal
	resolver := &routeMap{routes: map[string]forage.Route{route.Name: route}}
	adapter := &recordingAdapter{}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}
	for _, sensitivity := range []forage.Sensitivity{"", forage.SensitivityUnknown, forage.Sensitivity("unsupported")} {
		need := zeroCostNeed()
		need.Sensitivity = sensitivity
		_, err := dispatcher.Dispatch(context.Background(), []forage.Route{route}, forage.Request{Need: need})
		if !errors.Is(err, forage.ErrNoEligibleRoute) {
			t.Fatalf("sensitivity %q error = %v, want ErrNoEligibleRoute", sensitivity, err)
		}
	}
	if adapter.calls != 0 {
		t.Fatalf("Adapter.Chat calls = %d, want 0", adapter.calls)
	}
}

func TestDispatchUsesAuthoritativeRouteProvider(t *testing.T) {
	candidate := freeRoute()
	candidate.Provider = "openai"
	current := candidate
	current.Provider = "ollama"
	resolver := &routeMap{routes: map[string]forage.Route{candidate.Name: current}}
	staleAdapter := &recordingAdapter{}
	currentAdapter := &recordingAdapter{}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"openai": staleAdapter, "ollama": currentAdapter}}

	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{candidate}, forage.Request{Need: zeroCostNeed()}); err != nil {
		t.Fatal(err)
	}
	if staleAdapter.calls != 0 || currentAdapter.calls != 1 {
		t.Fatalf("calls stale=%d current=%d, want 0/1", staleAdapter.calls, currentAdapter.calls)
	}
}

func TestDispatchRejectsUnknownProviderBeforeChat(t *testing.T) {
	route := freeRoute()
	route.Provider = "unknown"
	resolver := &routeMap{routes: map[string]forage.Route{route.Name: route}}
	adapter := &recordingAdapter{}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}

	_, err := dispatcher.Dispatch(context.Background(), []forage.Route{route}, forage.Request{Need: zeroCostNeed()})
	if !errors.Is(err, forage.ErrNoEligibleRoute) || adapter.calls != 0 {
		t.Fatalf("Dispatch() = %v calls=%d, want ErrNoEligibleRoute and no calls", err, adapter.calls)
	}
}

func TestDispatchResolvesHeterogeneousProvidersToDifferentAdapters(t *testing.T) {
	openAI := freeRoute()
	openAI.Name, openAI.Provider = "openai", "openai-compatible"
	ollama := freeRoute()
	ollama.Name, ollama.Provider = "ollama", "ollama"
	resolver := &routeMap{routes: map[string]forage.Route{openAI.Name: openAI, ollama.Name: ollama}}
	openAIAdapter := &recordingAdapter{}
	ollamaAdapter := &recordingAdapter{}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"openai-compatible": openAIAdapter, "ollama": ollamaAdapter}}

	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{openAI}, forage.Request{Need: zeroCostNeed()}); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{ollama}, forage.Request{Need: zeroCostNeed()}); err != nil {
		t.Fatal(err)
	}
	if openAIAdapter.calls != 1 || ollamaAdapter.calls != 1 {
		t.Fatalf("adapter calls openai=%d ollama=%d, want 1/1", openAIAdapter.calls, ollamaAdapter.calls)
	}
}

func TestRouteValidateRejectsWhitespaceRequiredFields(t *testing.T) {
	fields := []struct {
		name string
		set  func(*forage.Route)
	}{
		{"name", func(r *forage.Route) { r.Name = " \t " }},
		{"provider", func(r *forage.Route) { r.Provider = " \t " }},
		{"model", func(r *forage.Route) { r.Model = " \t " }},
		{"endpoint", func(r *forage.Route) { r.Endpoint = " \t " }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			route := freeRoute()
			field.set(&route)
			if err := route.Validate(); err == nil {
				t.Fatalf("Validate() error = nil, want whitespace-only %s rejected", field.name)
			}
		})
	}
}

func TestRouteValidateRejectsUnsupportedPolicyEnums(t *testing.T) {
	route := freeRoute()
	route.CostClass = forage.CostClass("unsupported")
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unsupported cost rejected")
	}
	route = freeRoute()
	route.DataPolicy = forage.DataPolicy("unsupported")
	if err := route.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want unsupported data policy rejected")
	}
}

type routeMap struct{ routes map[string]forage.Route }

func (r *routeMap) Route(name string) (forage.Route, bool) {
	route, ok := r.routes[name]
	return route, ok
}

type recordingAdapter struct {
	calls int
	err   error
}

type adapterMap map[string]forage.Adapter

func (m adapterMap) Adapter(provider string) (forage.Adapter, bool) {
	a, ok := m[provider]
	return a, ok
}

func (a *recordingAdapter) Chat(context.Context, forage.Route, forage.Request) (forage.Response, error) {
	a.calls++
	return forage.Response{}, a.err
}
