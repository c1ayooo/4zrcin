//go:build !inprochost

package main

// The plain build does not embed the scan assembly, so there is no node verb
// to run. The verb is still recognized — and refused: falling through would
// hand `zrcin node …` to control-plane flag parsing, which either errors on
// the unknown argument or, worse, starts a server. The message names both ways
// out: an inprochost build for local mode, the scan binary for remote nodes.

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// isNodeVerb reports whether args name the node verb.
func isNodeVerb(args []string) bool {
	return len(args) > 0 && args[0] == "node"
}

// maybeNodeCLI takes over when args name the node verb and always leaves the
// process in this build: the verb exists only to be refused with a readable
// message and a non-zero exit.
func maybeNodeCLI(args []string) bool {
	if !isNodeVerb(args) {
		return false
	}
	if _, err := runNodeVerb(args, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
	return true
}

// runNodeVerb is the testable entry point: same refusal, caller-owned
// streams. handled is false only when the args do not name the verb.
func runNodeVerb(args []string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if !isNodeVerb(args) {
		return false, nil
	}
	return true, errors.New("此构建不带 scan 节点（节点装配只在 -tags inprochost 构建里）；本地模式请用 inprochost 构建的 zrcin，远程节点用 scan 二进制（cyber-scan）")
}
