package main

import (
	"os"
	"path/filepath"
)

// sessionEnvScript is sourced by every non-interactive POSIX shell the harness
// runs (BASH_ENV, and ENV for sh). It carries no credential: it locates the
// session's case from the working directory and exports the session's token and
// base URLs, so a model can use $TW_AUTH / $TW_BATTLE / $TW_API directly instead
// of reading and exporting files by hand. A missing script only costs that
// convenience; .tw/auth stays the fallback.
const sessionEnvScript = `# tianwu session environment (no secrets in this file).
_tw_dir=""
_tw_walk="$PWD"
while [ -n "$_tw_walk" ]; do
    if [ -f "$_tw_walk/.tw/identity.json" ]; then _tw_dir="$_tw_walk"; break; fi
    [ "$_tw_walk" = "/" ] && break
    _tw_walk=$(dirname "$_tw_walk")
done
if [ -n "$_tw_dir" ]; then
    _tw_id="$_tw_dir/.tw/identity.json"
    TW_API=$(sed -n 's/.*"api_base": *"\([^"]*\)".*/\1/p' "$_tw_id")
    TW_PROJECT=$(sed -n 's/.*"project_id": *"\([^"]*\)".*/\1/p' "$_tw_id")
    TW_SELF=$(sed -n 's/.*"intent_id": *"\([^"]*\)".*/\1/p' "$_tw_id")
    TW_AGENT="$TW_API/agent/$TW_PROJECT/api"
    TW_BATTLE="$TW_API/p/$TW_PROJECT/api"
    if [ -f "$_tw_dir/.tw/auth" ]; then
        TW_AUTH=$(cat "$_tw_dir/.tw/auth")
    else
        unset TW_AUTH
    fi
    export TW_API TW_PROJECT TW_SELF TW_AGENT TW_BATTLE TW_AUTH
fi
unset _tw_dir _tw_walk _tw_id
`

// sessionEnvPath is where the host's shell init lives. It is instance state,
// not per project: the script resolves the caller's case from $PWD.
func sessionEnvPath(dataDir string) string {
	return filepath.Join(dataDir, "harness", "session-env.sh")
}

// writeSessionEnv installs the shell init and returns its path. It rewrites only
// when the content changed, like the skill installer.
func writeSessionEnv(dataDir string) (string, error) {
	path := sessionEnvPath(dataDir)
	if cur, err := os.ReadFile(path); err == nil && string(cur) == sessionEnvScript {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(sessionEnvScript), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
