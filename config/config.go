// Package config loads the explicitly declared routes used by Forage.
package config

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the complete v0.0.1 configuration surface.
type Config struct {
	Routes []Route `yaml:"routes"`
}

// Route is a configuration-declared inference route. It deliberately has no
// discovery or provider-specific behavior.
type Route struct {
	Name         string       `yaml:"name"`
	Provider     string       `yaml:"provider"`
	Model        string       `yaml:"model"`
	Endpoint     string       `yaml:"endpoint"`
	Cost         string       `yaml:"cost"`
	DataPolicy   string       `yaml:"data_policy"`
	Capabilities Capabilities `yaml:"capabilities"`
}

// Capabilities contains the route properties that configuration may declare.
type Capabilities struct {
	Chat           bool `yaml:"chat"`
	JSONSchema     bool `yaml:"json_schema"`
	Usage          bool `yaml:"usage"`
	ReasoningUsage bool `yaml:"reasoning_usage"`
	ContextTokens  int  `yaml:"context_tokens"`
}

// Parse decodes and validates a configuration document.
func Parse(r io.Reader) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate confirms each route is explicitly and uniquely declared.
func (c Config) Validate() error {
	if len(c.Routes) == 0 {
		return fmt.Errorf("configuration must declare at least one route")
	}
	names := make(map[string]struct{}, len(c.Routes))
	for i, route := range c.Routes {
		if err := route.Validate(); err != nil {
			return fmt.Errorf("route %d: %w", i, err)
		}
		if _, exists := names[route.Name]; exists {
			return fmt.Errorf("route %d: duplicate name %q", i, route.Name)
		}
		names[route.Name] = struct{}{}
	}
	return nil
}

// Validate confirms a route has the identity required to be explicitly used.
func (r Route) Validate() error {
	for field, value := range map[string]string{
		"name":        r.Name,
		"provider":    r.Provider,
		"model":       r.Model,
		"endpoint":    r.Endpoint,
		"cost":        r.Cost,
		"data_policy": r.DataPolicy,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	return nil
}
