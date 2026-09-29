// Command forage validates an explicitly declared route configuration.
package main

import (
	"fmt"
	"os"

	"github.com/oorrwullie/forage/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("usage: forage <config.yaml> | forage serve --config <config.yaml> --listen <loopback:port>")
		return nil
	}
	if len(args) > 0 && args[0] == "serve" {
		return serve(args[1:])
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: forage <config.yaml> | forage serve --config <config.yaml> --listen <loopback:port>")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open configuration: %w", err)
	}
	_, parseErr := config.Parse(f)
	closeErr := f.Close()
	if parseErr != nil {
		return parseErr
	}
	if closeErr != nil {
		return fmt.Errorf("close configuration: %w", closeErr)
	}
	return nil
}
