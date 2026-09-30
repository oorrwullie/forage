package forage_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestEvaluateRejectsExcludedModel(t *testing.T) {
	route := freeRoute()
	need := zeroCostNeed()
	need.ExcludedModels = []string{route.Model}

	if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 0 {
		t.Fatalf("eligible = %#v, want excluded model rejection", got.Eligible)
	}
}

func TestEvaluateRejectsExcludedModelAcrossRoutes(t *testing.T) {
	first := freeRoute()
	second := freeRoute()
	second.Name = "same-model-other-route"
	second.Provider = "other"

	need := zeroCostNeed()
	need.ExcludedModels = []string{first.Model}

	if got := forage.Evaluate([]forage.Route{first, second}, need); len(got.Eligible) != 0 {
		t.Fatalf("eligible = %#v, want every route using excluded model rejected", got.Eligible)
	}
}

func TestEvaluatePinnedExcludedModelHasNoEligibleRoute(t *testing.T) {
	route := freeRoute()
	need := zeroCostNeed()
	need.PinnedModel = route.Model
	need.ExcludedModels = []string{route.Model}

	if got := forage.Evaluate([]forage.Route{route}, need); len(got.Eligible) != 0 {
		t.Fatalf("eligible = %#v, want pinned excluded model rejected", got.Eligible)
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

func TestDispatchRevalidationRejectsRouteMutatedToExcludedModel(t *testing.T) {
	route := freeRoute()
	need := zeroCostNeed()
	need.ExcludedModels = []string{"excluded-model"}

	resolver := &routeMap{routes: map[string]forage.Route{route.Name: route}}
	adapter := &recordingAdapter{}
	dispatcher := forage.Dispatcher{
		Routes:   resolver,
		Adapters: adapterMap{"test": adapter},
	}

	candidates := forage.Evaluate([]forage.Route{route}, need).Eligible
	if len(candidates) != 1 {
		t.Fatalf("initial candidates = %d, want 1", len(candidates))
	}

	mutated := route
	mutated.Model = "excluded-model"
	resolver.routes[route.Name] = mutated

	_, err := dispatcher.Dispatch(
		context.Background(),
		candidates,
		forage.Request{Need: need},
	)
	if !errors.Is(err, forage.ErrNoEligibleRoute) {
		t.Fatalf("Dispatch() error = %v, want ErrNoEligibleRoute", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter calls = %d, want 0", adapter.calls)
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

func TestDispatchFallsBackInCandidateOrder(t *testing.T) {
	first := freeRoute()
	second := first
	second.Name = "second"
	resolver := &routeMap{routes: map[string]forage.Route{first.Name: first, second.Name: second}}
	adapter := &scriptedAdapter{errs: []error{&forage.AdapterError{Kind: forage.ErrorProtocol}, nil}}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}

	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{first, second}, forage.Request{Need: zeroCostNeed()}); err != nil {
		t.Fatal(err)
	}
	if got := adapter.routes; len(got) != 2 || got[0] != first.Name || got[1] != second.Name {
		t.Fatalf("attempt order = %#v, want [%q %q]", got, first.Name, second.Name)
	}
}

func TestDispatchStopsForNonFallbackErrorsAndContext(t *testing.T) {
	first, second := twoFreeRoutes()
	for _, err := range []error{
		&forage.AdapterError{Kind: forage.ErrorAuth},
		errors.New("untyped"),
		context.Canceled,
		context.DeadlineExceeded,
	} {
		t.Run(err.Error(), func(t *testing.T) {
			adapter := &scriptedAdapter{errs: []error{err, nil}}
			dispatcher := fallbackDispatcher(first, second, adapter)
			_, got := dispatcher.Dispatch(context.Background(), []forage.Route{first, second}, forage.Request{Need: zeroCostNeed()})
			if !errors.Is(got, err) || adapter.calls != 1 {
				t.Fatalf("error=%v calls=%d, want first error and one call", got, adapter.calls)
			}
		})
	}
}

func TestDispatchHonorsAttemptBudgetAndReturnsLastFallbackError(t *testing.T) {
	first, second := twoFreeRoutes()
	third := second
	third.Name = "third"
	resolver := &routeMap{routes: map[string]forage.Route{first.Name: first, second.Name: second, third.Name: third}}
	firstErr := &forage.AdapterError{Kind: forage.ErrorUnavailable, RetryAfter: time.Hour}
	secondErr := &forage.AdapterError{Kind: forage.ErrorProtocol}
	for _, tc := range []struct {
		name           string
		max, wantCalls int
		want           error
	}{
		{"one", 1, 1, firstErr}, {"two", 2, 2, secondErr}, {"unlimited", 0, 3, secondErr}, {"negative unlimited", -1, 3, secondErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &scriptedAdapter{errs: []error{firstErr, secondErr, secondErr}}
			dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}
			_, got := dispatcher.Dispatch(context.Background(), []forage.Route{first, second, third}, forage.Request{Need: zeroCostNeed(), Budget: forage.Budget{MaxAttempts: tc.max}})
			if !errors.Is(got, tc.want) || adapter.calls != tc.wantCalls {
				t.Fatalf("error=%v calls=%d", got, adapter.calls)
			}
		})
	}
}

func TestDispatchSkipsStaleOrIneligibleRoutesWithoutConsumingAttempts(t *testing.T) {
	stale, eligible := twoFreeRoutes()
	stale.Name = "stale"
	paid := stale
	paid.CostClass = forage.CostPaid
	resolver := &routeMap{routes: map[string]forage.Route{stale.Name: paid, eligible.Name: eligible}}
	adapter := &scriptedAdapter{errs: []error{nil}}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}
	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{stale, eligible}, forage.Request{Need: zeroCostNeed(), Budget: forage.Budget{MaxAttempts: 1}}); err != nil || adapter.calls != 1 || adapter.routes[0] != eligible.Name {
		t.Fatalf("error=%v calls=%d routes=%#v", err, adapter.calls, adapter.routes)
	}
}

func TestDispatchFallbackRevalidatesAndResolvesCurrentProvider(t *testing.T) {
	first, second := twoFreeRoutes()
	second.Provider = "stale"
	current := second
	current.Provider = "current"
	resolver := &routeMap{routes: map[string]forage.Route{first.Name: first, second.Name: current}}
	firstAdapter := &scriptedAdapter{errs: []error{&forage.AdapterError{Kind: forage.ErrorEffectiveModelMismatch}}}
	currentAdapter := &scriptedAdapter{errs: []error{nil}}
	staleAdapter := &scriptedAdapter{}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": firstAdapter, "stale": staleAdapter, "current": currentAdapter}}
	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{first, second}, forage.Request{Need: zeroCostNeed()}); err != nil || firstAdapter.calls != 1 || staleAdapter.calls != 0 || currentAdapter.calls != 1 {
		t.Fatalf("error=%v calls first=%d stale=%d current=%d", err, firstAdapter.calls, staleAdapter.calls, currentAdapter.calls)
	}
}

func TestDispatchFailsClosedForUnknownAdapterAndNoAttempts(t *testing.T) {
	unknown, eligible := twoFreeRoutes()
	unknown.Provider = "unknown"
	resolver := &routeMap{routes: map[string]forage.Route{unknown.Name: unknown, eligible.Name: eligible}}
	adapter := &scriptedAdapter{errs: []error{nil}}
	dispatcher := forage.Dispatcher{Routes: resolver, Adapters: adapterMap{"test": adapter}}
	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{unknown, eligible}, forage.Request{Need: zeroCostNeed()}); !errors.Is(err, forage.ErrNoEligibleRoute) || adapter.calls != 0 {
		t.Fatalf("unknown adapter error=%v calls=%d", err, adapter.calls)
	}
	missing := freeRoute()
	resolver = &routeMap{routes: map[string]forage.Route{}}
	if _, err := dispatcher.Dispatch(context.Background(), []forage.Route{missing}, forage.Request{Need: zeroCostNeed()}); !errors.Is(err, forage.ErrNoEligibleRoute) {
		t.Fatalf("no attempts error=%v", err)
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

type scriptedAdapter struct {
	calls  int
	routes []string
	errs   []error
}

func (a *scriptedAdapter) Chat(_ context.Context, route forage.Route, _ forage.Request) (forage.Response, error) {
	a.routes = append(a.routes, route.Name)
	err := a.errs[a.calls]
	a.calls++
	return forage.Response{}, err
}

func twoFreeRoutes() (forage.Route, forage.Route) {
	first := freeRoute()
	second := first
	second.Name = "second"
	return first, second
}
func fallbackDispatcher(first, second forage.Route, adapter forage.Adapter) forage.Dispatcher {
	return forage.Dispatcher{Routes: &routeMap{routes: map[string]forage.Route{first.Name: first, second.Name: second}}, Adapters: adapterMap{"test": adapter}}
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
