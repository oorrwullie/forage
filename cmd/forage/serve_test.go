package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oorrwullie/forage"
)

func TestNewFacadeRejectsEmptyToken(t *testing.T) {
	_, err := newFacade([]forage.Route{testRoute("ollama", "http://example.test")}, "", "", http.DefaultClient)
	if err == nil {
		t.Fatal("newFacade() error = nil, want missing facade token rejected")
	}
}

func TestServeConfigurationRejectsUnsafeListenAddress(t *testing.T) {
	err := validateServeConfiguration("0.0.0.0:8080", "facade-token")
	if err == nil {
		t.Fatal("validateServeConfiguration() error = nil, want non-loopback address rejected")
	}
}

func TestNewFacadeComposesConfiguredOpenAIAdapterWithRuntimeCredential(t *testing.T) {
	var gotModel, gotAuthorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotModel = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"configured-model","choices":[{"message":{"role":"assistant","content":"done"}}]}`))
	}))
	defer provider.Close()
	route := testRoute("openai-compatible", provider.URL)
	route.Model = "configured-model"
	server, err := newFacade([]forage.Route{route}, "facade-token", "runtime-provider-token", provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{"input":"hello","need":{"sensitivity":"public","require_chat":true}}`))
	req.Header.Set("Authorization", "Bearer facade-token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if gotAuthorization != "Bearer runtime-provider-token" || !strings.Contains(gotModel, `"model":"configured-model"`) {
		t.Fatalf("provider authorization=%q request=%q, want runtime token and configured route model", gotAuthorization, gotModel)
	}
}

func TestNewFacadeComposesOllamaWithoutCredential(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("Ollama authorization = %q, want none", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"configured-model","message":{"role":"assistant","content":"done"},"done":true}`))
	}))
	defer provider.Close()
	route := testRoute("ollama", provider.URL)
	route.Model = "configured-model"
	server, err := newFacade([]forage.Route{route}, "facade-token", "", provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{"input":"hello","need":{"sensitivity":"public","require_chat":true}}`))
	req.Header.Set("Authorization", "Bearer facade-token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestNewFacadeLeavesUnsupportedProviderFailClosed(t *testing.T) {
	server, err := newFacade([]forage.Route{testRoute("unsupported", "http://example.test")}, "facade-token", "", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{"input":"hello","need":{"sensitivity":"public","require_chat":true}}`))
	req.Header.Set("Authorization", "Bearer facade-token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for unsupported provider", rr.Code)
	}
}

func testRoute(provider, endpoint string) forage.Route {
	return forage.Route{
		Name: "configured-route", Provider: provider, Model: "configured-model", Endpoint: endpoint,
		CostClass: forage.CostFree, DataPolicy: forage.DataPolicyNoTrain,
		Capabilities: forage.Capabilities{Chat: true, ContextTokens: 4096},
	}
}
