package main

import (
	"path/filepath"

	"github.com/c1ayooo/4zrcin/internal/settings"
)

// runtimeHarnessArgs is shared by boot and settings-driven restarts. Passing an
// explicit, filtered document prevents the harness from discovering and parsing
// the operator's file, where 4zrcin keeps metadata such as profile pause state.
func runtimeHarnessArgs(st *settings.Store, dataDir string, m settings.Model) ([]string, string, error) {
	hubPath, err := st.RuntimeConfig(filepath.Join(dataDir, "host", "config"))
	if err != nil {
		return nil, "", err
	}
	nodePath, err := st.NodeRuntimeConfig(filepath.Join(dataDir, "host", "config"))
	if err != nil {
		return nil, "", err
	}
	// The Hub document (Args) carries shared sections only; the node document
	// carries the product sections top-level. One registry accepts them, the
	// other rejects them — two files since the upstream gateway rework.
	return append([]string{"--config", hubPath}, m.Args()...), nodePath, nil
}
