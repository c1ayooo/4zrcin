package main

import (
	"github.com/c1ayooo/4zrcin/internal/api"
	"github.com/c1ayooo/4zrcin/internal/enroll"
	"github.com/c1ayooo/4zwebshell/webshell"
)

// wireShells hands a project server the two halves of the webshell surface
// (docs/18): the enrollment service, whose directive channel runs an operation
// on the shell's node, and the sealer that keeps every credential ciphertext
// in the ledger. The sealing key follows the instance secret, so a restart
// opens what the previous process sealed.
//
// Without --public-url there is no ingress and no remote nodes: the runner is
// left unset and the shell routes answer 503 rather than running anything.
func wireShells(srv *api.Server, nodes *enroll.Service, instanceSecret string) {
	if nodes != nil {
		srv.ShellRunner = nodes
	}
	srv.ShellSealer = webshell.NewSealer([]byte(instanceSecret))
}
