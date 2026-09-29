package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/oorrwullie/forage"
	"github.com/oorrwullie/forage/config"
	"github.com/oorrwullie/forage/facade"
	"github.com/oorrwullie/forage/provider/ollama"
	"github.com/oorrwullie/forage/provider/openai"
)

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "route configuration path")
	listenAddress := flags.String("listen", "", "loopback listen address")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *configPath == "" || *listenAddress == "" {
		return errors.New("usage: forage serve --config <config.yaml> --listen <loopback:port>")
	}
	if err := validateServeConfiguration(*listenAddress, os.Getenv("FORAGE_FACADE_TOKEN")); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	server, err := newFacade(cfg.Routes, os.Getenv("FORAGE_FACADE_TOKEN"), os.Getenv("FORAGE_OPENAI_BEARER_TOKEN"), http.DefaultClient)
	if err != nil {
		return err
	}
	return http.ListenAndServe(*listenAddress, server.Handler())
}

func validateServeConfiguration(listenAddress, facadeToken string) error {
	if facadeToken == "" {
		return errors.New("facade bearer token is required")
	}
	return facade.ValidateListenAddress(listenAddress)
}

func loadConfig(path string) (config.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer f.Close()
	cfg, err := config.Parse(f)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func newFacade(routes []forage.Route, facadeToken, openAIBearerToken string, client *http.Client) (*facade.Server, error) {
	if client == nil {
		return nil, errors.New("HTTP client is required")
	}
	routeByName := make(map[string]forage.Route, len(routes))
	adapters := make(map[string]forage.Adapter)
	for _, route := range routes {
		routeByName[route.Name] = route
		if _, exists := adapters[route.Provider]; exists {
			continue
		}
		switch route.Provider {
		case "ollama":
			adapters[route.Provider] = ollama.New(client)
		case "openai-compatible":
			adapters[route.Provider] = openai.New(client, openAIBearerToken)
		}
	}
	return facade.New(routes, forage.Dispatcher{
		Routes:   routeResolver{routes: routeByName},
		Adapters: adapterResolver{adapters: adapters},
	}, facadeToken)
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
