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
	if len(args) != 1 {
		return fmt.Errorf("usage: forage <config.yaml>")
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
