//go:build inprochost

package main

// zrcin node …: this build's own executable IS the scan node. The engine's
// scan assembly lives in pkg/assembly (docs/19 P6, G6), so the node process
// zrcin spawns as its local host's scan half runs exactly the command line
// cyber-scan would — same agent subcommand, same flags, same registration
// hello. Only the process shell differs, and this file is that shell.
//
// Dispatch happens before server flags are ever parsed (see main): a known
// verb is a CLI call that runs and exits; `zrcin node …` must never fall
// through to control-plane flag parsing, because the node is a long-lived
// process the host supervisor owns.

import (
	"fmt"
	"io"
	"os"

	"github.com/chainreactors/cyber/pkg/assembly"
)

// isNodeVerb reports whether args name the node verb.
func isNodeVerb(args []string) bool {
	return len(args) > 0 && args[0] == "node"
}

// maybeNodeCLI takes over when args name the node verb. Like the scan
// binary's main, an error prints `error: …` and exits 1; success returns and
// main is done — the caller of this process (the host supervisor) sees the
// exit code, not the control plane's flag parser.
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

// runNodeVerb is the testable entry point: same behaviour, caller-owned
// streams. args keeps the verb in [0]; everything after it is the scan command
// line itself (assembly.Exec takes args without the program name). handled is
// false only when the args do not name the verb, so callers can chain it.
func runNodeVerb(args []string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if !isNodeVerb(args) {
		return false, nil
	}
	return true, assembly.Exec(args[1:], stdin, stdout, stderr)
}
