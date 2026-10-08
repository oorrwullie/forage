package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/oorrwullie/forage"
	"github.com/oorrwullie/forage/config"
)

func TestParseAcceptsExplicitRoute(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`
routes:
  - name: local-review
    provider: local
    model: test-model
    endpoint: http://127.0.0.1:11434
    cost: free
    data_policy: local
    capabilities:
      chat: true
      context_tokens: 8192
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Name != "local-review" {
		t.Fatalf("Parse() routes = %#v", cfg.Routes)
	}
}

func TestIdentityUsesAcceptedSemanticsDeterministically(t *testing.T) {
	first, err := config.Parse(strings.NewReader(`
routes:
  - name: local-review
    provider: ollama
    model: test-model
    endpoint: http://127.0.0.1:11434/api/chat
    cost: free
    data_policy: local
    capabilities:
      chat: true
      context_tokens: 8192
`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := config.Parse(strings.NewReader(`
routes: [{capabilities: {context_tokens: 8192, chat: true}, data_policy: local,
  cost: free, endpoint: http://127.0.0.1:11434/api/chat, model: test-model,
  provider: ollama, name: local-review}]
`))
	if err != nil {
		t.Fatal(err)
	}
	firstID, err := first.Identity()
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := second.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID || len(firstID) != 64 {
		t.Fatalf("identities = %q, %q; want equal SHA-256 hex", firstID, secondID)
	}

	changed := first
	changed.Routes = append([]forage.Route(nil), first.Routes...)
	changed.Routes[0].Model = "different-model"
	changedID, err := changed.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if changedID == firstID {
		t.Fatal("meaningful route change did not change identity")
	}
}

func TestIdentityExcludesRuntimeSecretsAndRejectsInvalidConfig(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`
routes:
  - name: route
    provider: ollama
    model: model
    endpoint: http://127.0.0.1:11434/api/chat
    cost: free
    data_policy: local
`))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORAGE_FACADE_TOKEN", "first-secret")
	first, err := cfg.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("FORAGE_FACADE_TOKEN", "second-secret"); err != nil {
		t.Fatal(err)
	}
	second, err := cfg.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("runtime secret changed configuration identity")
	}

	cfg.Routes[0].Model = ""
	if identity, err := cfg.Identity(); err == nil || identity != "" {
		t.Fatalf("invalid Identity() = %q, %v; want no identity and error", identity, err)
	}
}

func TestParseRejectsRouteMissingModel(t *testing.T) {
	_, err := config.Parse(strings.NewReader(`
routes:
  - name: incomplete
    provider: local
    endpoint: http://127.0.0.1:11434
    cost: free
    data_policy: local
`))
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("Parse() error = %v, want model validation error", err)
	}
}

func TestParseAcceptsHeterogeneousExplicitFreeRoutes(t *testing.T) {
	cfg, err := config.Parse(strings.NewReader(`
routes:
  - name: openai-free
    provider: openai-compatible
    model: free-openai
    endpoint: http://127.0.0.1:8080/v1/chat/completions
    cost: free
    data_policy: no-train
    capabilities: {chat: true, context_tokens: 4096}
  - name: ollama-free
    provider: ollama
    model: qwen3-coder:30b
    endpoint: http://127.0.0.1:11434/api/chat
    cost: free
    data_policy: local
    capabilities: {chat: true, context_tokens: 4096}
`))
	if err != nil || len(cfg.Routes) != 2 {
		t.Fatalf("Parse() = %#v, %v", cfg, err)
	}
}
