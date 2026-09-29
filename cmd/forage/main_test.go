package main

import "testing"

func TestRunRequiresConfigPath(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("run(nil) error = nil, want config-path error")
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			if err := run([]string{arg}); err != nil {
				t.Fatalf("run(%q): %v", arg, err)
			}
		})
	}
}
