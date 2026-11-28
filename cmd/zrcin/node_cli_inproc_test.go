//go:build inprochost

package main

// Only the inprochost build can run the node verb: this file proves the
// embedded assembly answers the version probe the same way the scan binary
// does ("which node is this" must be answerable from either entry point).

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunNodeVerbVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	handled, err := runNodeVerb([]string{"node", "--version"}, bytes.NewReader(nil), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runNodeVerb(--version): %v", err)
	}
	if !handled {
		t.Fatal("runNodeVerb(--version) 未接管：node 动词必须运行并退出，不能落回控制面 flag 解析")
	}
	if out := stdout.String(); !strings.HasPrefix(out, "cyber-scan v") {
		t.Fatalf("版本输出以 %q 开头，期望前缀 cyber-scan v", out)
	}
}
