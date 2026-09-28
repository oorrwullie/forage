package config_test

import (
	"strings"
	"testing"

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
