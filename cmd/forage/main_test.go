package main

import "testing"

func TestRunRequiresConfigPath(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("run(nil) error = nil, want config-path error")
	}
}
