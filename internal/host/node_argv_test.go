package host

// nodeArgv joins the node prefix with the scan arguments. Pure function, no
// build tag: the exec and inproc launch paths and both build flavors share it.

import (
	"reflect"
	"testing"
)

func TestNodeArgv(t *testing.T) {
	tests := []struct {
		name   string
		prefix []string
		args   []string
		want   []string
	}{
		{"no prefix keeps args verbatim", nil, []string{"agent", "--transport", "web"}, []string{"agent", "--transport", "web"}},
		{"empty prefix keeps args verbatim", []string{}, []string{"agent"}, []string{"agent"}},
		{"self node prepends the verb", []string{"node"}, []string{"agent", "--transport", "web"}, []string{"node", "agent", "--transport", "web"}},
		{"prefix longer than one token", []string{"node", "--extra"}, []string{"agent"}, []string{"node", "--extra", "agent"}},
		{"empty args", []string{"node"}, nil, []string{"node"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeArgv(tc.prefix, tc.args); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("nodeArgv(%v, %v) = %v, want %v", tc.prefix, tc.args, got, tc.want)
			}
		})
	}
}
