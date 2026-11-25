//go:build !inprochost

package inproc

import (
	"context"
	"errors"
	"net"
)

// Enabled is false: this build does not embed the Hub, so the host runs the
// exec form and every Inproc launch attempt fails with a named error instead
// of an import.
const Enabled = false

// Run is never called in this form — host.Start refuses Inproc before
// reaching it. The stub keeps the tagged-out call sites compiling.
func Run(ctx context.Context, listener net.Listener, args []string, logf func(format string, args ...any)) error {
	return errors.New("此构建未内嵌 Hub：用 -tags inprochost 构建，或去掉进程内模式")
}
