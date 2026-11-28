package main

import (
	"reflect"
	"strings"
	"testing"
)

// The gate forwards the harness build to the engine checkout's own Makefile
// (docs/19 §7 build convergence): the engine owns the tag set, the shell only
// names the two goals and the output directory. The invocation is a pure
// function so this table can pin it.
func TestHarnessMakeSpec(t *testing.T) {
	cases := []struct {
		name      string
		engineDir string
		binDir    string
		goProxy   string
		wantArgv  []string
		wantEnv   []string
	}{
		{
			name:      "no GOPROXY supplies the default mirror",
			engineDir: "/opt/cyber-harness",
			binDir:    "/repo/bin",
			goProxy:   "",
			wantArgv:  []string{"make", "-C", "/opt/cyber-harness", "scan", "BIN_DIR=/repo/bin"},
			wantEnv:   []string{"GOPROXY=" + goproxyDefault},
		},
		{
			name:      "a blank GOPROXY counts as unset",
			engineDir: "/opt/cyber-harness",
			binDir:    "/repo/bin",
			goProxy:   "   ",
			wantArgv:  []string{"make", "-C", "/opt/cyber-harness", "scan", "BIN_DIR=/repo/bin"},
			wantEnv:   []string{"GOPROXY=" + goproxyDefault},
		},
		{
			name:      "an operator GOPROXY wins",
			engineDir: "/opt/cyber-harness",
			binDir:    "/repo/bin",
			goProxy:   "https://proxy.example,direct",
			wantArgv:  []string{"make", "-C", "/opt/cyber-harness", "scan", "BIN_DIR=/repo/bin"},
			wantEnv:   nil,
		},
		{
			name:      "engine dir and bin dir land in the argv verbatim",
			engineDir: "/home/me/cyber-harness",
			binDir:    "/home/me/4zrcin/bin",
			goProxy:   "direct",
			wantArgv:  []string{"make", "-C", "/home/me/cyber-harness", "scan", "BIN_DIR=/home/me/4zrcin/bin"},
			wantEnv:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv, env := harnessMakeSpec(tc.engineDir, tc.binDir, tc.goProxy)
			if !reflect.DeepEqual(argv, tc.wantArgv) {
				t.Fatalf("argv = %v, want %v", argv, tc.wantArgv)
			}
			if !reflect.DeepEqual(env, tc.wantEnv) {
				t.Fatalf("env = %v, want %v", env, tc.wantEnv)
			}
		})
	}
}

// buildHarness must fail with actionable guidance when make is absent: the
// forward depends on it, and a bare exec error would not say who needs it.
func TestBuildHarnessWithoutMake(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // an empty PATH: no make anywhere
	err := buildHarness("/opt/cyber-harness", dir)
	if err == nil {
		t.Fatal("buildHarness must fail when make is missing")
	}
	if !strings.Contains(err.Error(), "make") || !strings.Contains(err.Error(), "安装") {
		t.Fatalf("the error should name make and the fix, got: %v", err)
	}
}
