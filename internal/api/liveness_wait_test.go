package api

import "time"

// livenessWait bounds the "it has to happen at all" waits in this package's
// tests: a host has to see a frame, a native command has to start, a goroutine
// has to finish. It is a liveness guard, not the assertion — the test still
// fails when the event never happens — so a bound tight enough to be crossed by
// scheduling noise only produces false reds.
//
// Three seconds was crossed for real: this suite runs parallel and shares the
// host with acceptance runs, and one red landed at 3.07s while the command was
// merely queued. A false red costs a re-run plus somebody's judgement; the ten
// seconds here cost nothing when the event happens on time.
const livenessWait = 10 * time.Second
