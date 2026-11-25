package main

import (
	"os"
	"path/filepath"
	"strings"
)

// sessionEnvScript is sourced by every non-interactive POSIX shell the harness
// runs (BASH_ENV, and ENV for sh). It carries no credential: it locates the
// session's case from the working directory and exports the session's token and
// base URLs, so a model can use $ZRCIN_AUTH / $ZRCIN_BATTLE / $ZRCIN_API directly instead
// of reading and exporting files by hand. A missing script only costs that
// convenience; .zrcin/auth stays the fallback.
//
// ZRCIN_BIN is the running zrcin executable, exported so a model can call the native
// read verbs (`"$ZRCIN_BIN" board`, `"$ZRCIN_BIN" wake`) instead of hand-writing curl.
// The path is substituted at install time (writeSessionEnv): it differs per
// machine and a stale path from .zrcin must never shadow it.
const sessionEnvScript = `# 4zrcin session environment (no secrets in this file).
ZRCIN_BIN=@ZRCIN_BIN@
export ZRCIN_BIN
_tw_dir=""
_tw_walk="$PWD"
while [ -n "$_tw_walk" ]; do
    if [ -f "$_tw_walk/.zrcin/identity.json" ]; then _tw_dir="$_tw_walk"; break; fi
    [ "$_tw_walk" = "/" ] && break
    _tw_walk=$(dirname "$_tw_walk")
done
if [ -n "$_tw_dir" ]; then
    _tw_id="$_tw_dir/.zrcin/identity.json"
    ZRCIN_API=$(sed -n 's/.*"api_base": *"\([^"]*\)".*/\1/p' "$_tw_id")
    ZRCIN_PROJECT=$(sed -n 's/.*"project_id": *"\([^"]*\)".*/\1/p' "$_tw_id")
    ZRCIN_SELF=$(sed -n 's/.*"intent_id": *"\([^"]*\)".*/\1/p' "$_tw_id")
    ZRCIN_AGENT="$ZRCIN_API/agent/$ZRCIN_PROJECT/api"
    ZRCIN_BATTLE="$ZRCIN_API/p/$ZRCIN_PROJECT/api"
    if [ -f "$_tw_dir/.zrcin/auth" ]; then
        ZRCIN_AUTH=$(cat "$_tw_dir/.zrcin/auth")
    else
        unset ZRCIN_AUTH
    fi
    export ZRCIN_API ZRCIN_PROJECT ZRCIN_SELF ZRCIN_AGENT ZRCIN_BATTLE ZRCIN_AUTH
fi
unset _tw_dir _tw_walk _tw_id
`

// twBinPlaceholder is replaced with the shell-quoted executable path. It is not
// inside quotes in the template: the quoting is part of the substitution.
const twBinPlaceholder = "@ZRCIN_BIN@"

// sessionEnvContent renders the shell init for this executable. os.Executable
// failure leaves ZRCIN_BIN empty: the verb still works with --base/--token, and the
// skill's curl fallback covers the rest.
func sessionEnvContent() string {
	bin := ""
	if p, err := os.Executable(); err == nil {
		if abs, absErr := filepath.Abs(p); absErr == nil {
			p = abs
		}
		// Git Bash on Windows takes forward slashes; a backslash path is read as
		// escapes. ToSlash is a no-op on POSIX.
		bin = filepath.ToSlash(p)
	}
	return strings.Replace(sessionEnvScript, twBinPlaceholder, shellSingleQuote(bin), 1)
}

// shellSingleQuote wraps s in single quotes for a POSIX shell, escaping any
// embedded quote the standard way. Empty stays an explicit empty string.
func shellSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sessionEnvPath is where the host's shell init lives. It is instance state,
// not per project: the script resolves the caller's case from $PWD.
func sessionEnvPath(dataDir string) string {
	return filepath.Join(dataDir, "harness", "session-env.sh")
}

// writeSessionEnv installs the shell init and returns its path. It rewrites only
// when the content changed, like the skill installer; the content embeds this
// binary's path, so a moved/rebuilt zrcin updates it exactly once.
func writeSessionEnv(dataDir string) (string, error) {
	path := sessionEnvPath(dataDir)
	content := sessionEnvContent()
	if cur, err := os.ReadFile(path); err == nil && string(cur) == content {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
