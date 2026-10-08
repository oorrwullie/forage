// Package config loads the explicitly declared routes used by Forage.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/oorrwullie/forage"
	"gopkg.in/yaml.v3"
)

// Config is the complete v0.0.1 configuration surface.
type Config struct {
	Routes []forage.Route `json:"routes" yaml:"routes"`
}

// Identity returns the SHA-256 identity of the accepted configuration. The
// canonical form is JSON encoding of Config: route order is preserved (it is
// fallback order), object field order follows the declared Go structs, and
// YAML presentation details are discarded.
func (c Config) Identity() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("canonicalize configuration: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
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
