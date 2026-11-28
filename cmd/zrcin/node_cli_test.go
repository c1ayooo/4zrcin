package main

// The node verb's dispatch predicate is shared by both build flavors, so its
// table lives in the untagged test file: a regression here must fail the
// default build too, not only the inprochost one.

import "testing"

func TestIsNodeVerb(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"bare verb", []string{"node"}, true},
		{"verb with args", []string{"node", "agent", "--transport", "web"}, true},
		{"version", []string{"node", "--version"}, true},
		{"other verb", []string{"shell", "run"}, false},
		{"empty args", nil, false},
		{"prefix is not the verb", []string{"nodejs", "run"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNodeVerb(tc.args); got != tc.want {
				t.Fatalf("isNodeVerb(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
