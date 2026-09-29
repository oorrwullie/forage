package facade_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oorrwullie/forage"
	"github.com/oorrwullie/forage/facade"
)

func TestNewRejectsEmptyBearerToken(t *testing.T) {
	_, err := facade.New(nil, forage.Dispatcher{}, "")
	if err == nil {
		t.Fatal("New() error = nil, want empty token rejected")
	}
}

func TestHandlerAuthenticationPrecedesExecution(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth string
	}{
		{"missing", ""},
		{"wrong scheme", "Basic facade-token"},
		{"wrong token", "Bearer wrong-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &recordingAdapter{}
			server := newServer(t, freeRoute(), adapter)
			req := request(http.MethodPost, "/v1/chat", tc.auth, `{"input":"hello","need":{"sensitivity":"public","require_chat":true}}`)
			rr := httptest.NewRecorder()

			server.Handler().ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized || adapter.calls != 0 {
				t.Fatalf("status=%d calls=%d, want 401 and no execution", rr.Code, adapter.calls)
			}
			if strings.Contains(rr.Body.String(), "facade-token") {
				t.Fatalf("response leaked token: %q", rr.Body.String())
			}
		})
	}
}

func TestHandlerHTTPShapeValidationPrecedesExecution(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, contentType, body string
		want                                  int
	}{
		{"unknown path", http.MethodPost, "/missing", "application/json", `{}`, http.StatusNotFound},
		{"wrong method", http.MethodGet, "/v1/chat", "application/json", `{}`, http.StatusMethodNotAllowed},
		{"unsupported media type", http.MethodPost, "/v1/chat", "text/plain", `{}`, http.StatusUnsupportedMediaType},
		{"malformed JSON", http.MethodPost, "/v1/chat", "application/json", `{`, http.StatusBadRequest},
		{"trailing JSON", http.MethodPost, "/v1/chat", "application/json", `{} {}`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/v1/chat", "application/json", `{"misspelled_policy":true}`, http.StatusBadRequest},
		{"caller candidate authority", http.MethodPost, "/v1/chat", "application/json", `{"candidate_routes":[]}`, http.StatusBadRequest},
		{"caller provider authority", http.MethodPost, "/v1/chat", "application/json", `{"provider":"attacker"}`, http.StatusBadRequest},
		{"caller endpoint authority", http.MethodPost, "/v1/chat", "application/json", `{"endpoint":"http://attacker.test"}`, http.StatusBadRequest},
		{"caller cost authority", http.MethodPost, "/v1/chat", "application/json", `{"cost_class":"free"}`, http.StatusBadRequest},
		{"caller data authority", http.MethodPost, "/v1/chat", "application/json", `{"data_policy":"local"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &recordingAdapter{}
			server := newServer(t, freeRoute(), adapter)
			req := request(tc.method, tc.path, "Bearer facade-token", tc.body)
			req.Header.Set("Content-Type", tc.contentType)
			rr := httptest.NewRecorder()

			server.Handler().ServeHTTP(rr, req)

			if rr.Code != tc.want || adapter.calls != 0 {
				t.Fatalf("status=%d calls=%d, want %d and no execution", rr.Code, adapter.calls, tc.want)
			}
		})
	}
}

func TestHandlerAcceptsJSONParametersAndReturnsCanonicalResponse(t *testing.T) {
	adapter := &recordingAdapter{response: forage.Response{
		Output:    "done",
		Execution: forage.Execution{Route: "free", Provider: "test", RequestedModel: "requested", EffectiveModel: "effective", Usage: forage.Usage{InputTokens: 1, OutputTokens: 2, ReasoningTokens: 3}},
	}}
	server := newServer(t, freeRoute(), adapter)
	req := request(http.MethodPost, "/v1/chat", "Bearer facade-token", `{"input":"hello","need":{"zero_cost":true,"sensitivity":"public","require_chat":true},"budget":{"max_attempts":2}}`)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || adapter.calls != 1 {
		t.Fatalf("status=%d calls=%d, want 200 and one execution", rr.Code, adapter.calls)
	}
	for _, want := range []string{`"output":"done"`, `"route":"free"`, `"provider":"test"`, `"requested_model":"requested"`, `"effective_model":"effective"`, `"input_tokens":1`, `"reasoning_tokens":3`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("response %q missing %q", rr.Body.String(), want)
		}
	}
}

func TestHandlerFailsClosedForPolicyAndOversizedBodies(t *testing.T) {
	paid := freeRoute()
	paid.CostClass = forage.CostPaid
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"unset sensitivity", `{"input":"hello","need":{"zero_cost":true,"require_chat":true}}`, http.StatusUnprocessableEntity},
		{"unknown sensitivity", `{"input":"hello","need":{"zero_cost":true,"sensitivity":"unknown","require_chat":true}}`, http.StatusUnprocessableEntity},
		{"paid zero cost", `{"input":"hello","need":{"zero_cost":true,"sensitivity":"public","require_chat":true}}`, http.StatusUnprocessableEntity},
		{"oversized", `{"input":"` + strings.Repeat("x", facade.MaxRequestBodyBytes) + `","need":{"sensitivity":"public"}}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &recordingAdapter{}
			route := freeRoute()
			if tc.name == "paid zero cost" {
				route = paid
			}
			server := newServer(t, route, adapter)
			req := request(http.MethodPost, "/v1/chat", "Bearer facade-token", tc.body)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			server.Handler().ServeHTTP(rr, req)

			if rr.Code != tc.want || adapter.calls != 0 {
				t.Fatalf("status=%d calls=%d, want %d and no execution", rr.Code, adapter.calls, tc.want)
			}
		})
	}
}

func TestHandlerMapsDispatchFailureWithoutLeakingProviderError(t *testing.T) {
	adapter := &recordingAdapter{err: errors.New("provider secret failure")}
	server := newServer(t, freeRoute(), adapter)
	req := request(http.MethodPost, "/v1/chat", "Bearer facade-token", `{"input":"hello","need":{"sensitivity":"public","require_chat":true}}`)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway || adapter.calls != 1 {
		t.Fatalf("status=%d calls=%d, want 502 and one execution", rr.Code, adapter.calls)
	}
	if strings.Contains(rr.Body.String(), "provider secret failure") || strings.Contains(rr.Body.String(), "facade-token") {
		t.Fatalf("response leaked sensitive detail: %q", rr.Body.String())
	}
}

func TestHandlerLeavesFallbackToDispatcher(t *testing.T) {
	first := freeRoute()
	first.Name = "first"
	second := freeRoute()
	second.Name = "second"
	adapter := &recordingAdapter{errs: []error{&forage.AdapterError{Kind: forage.ErrorProtocol}, nil}}
	server, err := facade.New([]forage.Route{first, second}, forage.Dispatcher{
		Routes:   routeResolver{routes: map[string]forage.Route{first.Name: first, second.Name: second}},
		Adapters: adapterResolver{adapters: map[string]forage.Adapter{"test": adapter}},
	}, "facade-token")
	if err != nil {
		t.Fatal(err)
	}
	req := request(http.MethodPost, "/v1/chat", "Bearer facade-token", `{"input":"hello","need":{"sensitivity":"public","require_chat":true}}`)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || adapter.calls != 2 {
		t.Fatalf("status=%d calls=%d, want Dispatcher-owned fallback success", rr.Code, adapter.calls)
	}
}

func TestValidateListenAddress(t *testing.T) {
	for _, tc := range []struct {
		address string
		wantOK  bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"192.0.2.1:8080", false},
		{"example.test:8080", false},
		{"127.0.0.1", false},
		{":8080", false},
		{"127.0.0.1:", false},
	} {
		t.Run(tc.address, func(t *testing.T) {
			err := facade.ValidateListenAddress(tc.address)
			if (err == nil) != tc.wantOK {
				t.Fatalf("ValidateListenAddress(%q) error=%v, wantOK=%t", tc.address, err, tc.wantOK)
			}
		})
	}
}

func newServer(t *testing.T, route forage.Route, adapter forage.Adapter) *facade.Server {
	t.Helper()
	server, err := facade.New([]forage.Route{route}, forage.Dispatcher{
		Routes:   routeResolver{routes: map[string]forage.Route{route.Name: route}},
		Adapters: adapterResolver{adapters: map[string]forage.Adapter{route.Provider: adapter}},
	}, "facade-token")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func request(method, path, authorization, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return req
}

func freeRoute() forage.Route {
	return forage.Route{Name: "free", Provider: "test", Model: "free-model", Endpoint: "http://example.test", CostClass: forage.CostFree, DataPolicy: forage.DataPolicyNoTrain, Capabilities: forage.Capabilities{Chat: true, ContextTokens: 4096}}
}

type routeResolver struct{ routes map[string]forage.Route }

func (r routeResolver) Route(name string) (forage.Route, bool) {
	route, ok := r.routes[name]
	return route, ok
}

type adapterResolver struct{ adapters map[string]forage.Adapter }

func (r adapterResolver) Adapter(provider string) (forage.Adapter, bool) {
	adapter, ok := r.adapters[provider]
	return adapter, ok
}

type recordingAdapter struct {
	calls    int
	response forage.Response
	err      error
	errs     []error
}

func (a *recordingAdapter) Chat(context.Context, forage.Route, forage.Request) (forage.Response, error) {
	if a.calls < len(a.errs) {
		err := a.errs[a.calls]
		a.calls++
		return a.response, err
	}
	a.calls++
	return a.response, a.err
}
