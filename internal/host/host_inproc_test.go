//go:build inprochost

package host

// The embedded build's default node is the control plane's own executable
// (docs/19 §2 本地模式：一个文件、两个进程). These tests pin the resolution
// contract: the default resolves to this binary plus the node verb prefix; an
// explicit --scan path wins unchanged and carries no prefix (the escape hatch
// for read-only mounts and no-exec environments).

import (
	"os"
	"testing"
)

func TestResolveSelfNodeDefaultIsThisExecutable(t *testing.T) {
	bin, prefix, err := resolveSelfNode(Config{})
	if err != nil {
		t.Fatalf("resolveSelfNode(空 NodeBin): %v", err)
	}
	want, wantErr := os.Executable()
	if wantErr != nil {
		t.Skipf("os.Executable 不可用：%v", wantErr)
	}
	if bin != want {
		t.Fatalf("默认节点 = %q，期望本可执行文件 %q", bin, want)
	}
	if len(prefix) != 1 || prefix[0] != "node" {
		t.Fatalf("默认前缀 = %v，期望 [node]（节点 argv 需要先过 node 动词）", prefix)
	}
}

func TestResolveSelfNodeExplicitScanBinWins(t *testing.T) {
	const external = "/opt/cyber-scan"
	bin, prefix, err := resolveSelfNode(Config{NodeBin: external})
	if err != nil {
		t.Fatalf("resolveSelfNode(显式 NodeBin): %v", err)
	}
	if bin != external {
		t.Fatalf("显式节点被改写为 %q，期望原样保留 %q", bin, external)
	}
	if prefix != nil {
		t.Fatalf("显式节点不应带前缀，实际 %v（外部 cyber-scan 不认识 node 动词）", prefix)
	}
}
