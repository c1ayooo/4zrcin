// Package platform holds the Windows/Linux equivalences the rest of the tree
// depends on: executable naming, path folding and the isolation boundary
// declaration.
//
// Every function here is pure and takes the target GOOS explicitly instead of
// reading runtime.GOOS. That is deliberate: a Windows-only path rule and its
// Linux counterpart have to be testable from either host, so no branch can be
// added without a test that runs on both platforms. Callers on the real host
// pass runtime.GOOS (see Current); tests pass "windows" and "linux" in the same
// process.
package platform

import (
	"runtime"
	"strings"
)

// GOOS values used by the equivalences here. They are the Go names, not a
// separate enum, so the value recorded in a build manifest or boundary response
// matches `go env GOOS`.
const (
	Windows = "windows"
	Linux   = "linux"
)

// Descriptor is the naming/runtime convention of one target platform. It is a
// plain data struct: it can be serialized into status responses without a
// second platform switch on the consumer side.
type Descriptor struct {
	GOOS   string `json:"os"`
	GOARCH string `json:"arch"`
	// Family is "windows" or "posix"; POSIX is the conservative default for
	// every GOOS this project does not special-case.
	Family string `json:"family"`
	// EXESuffix is ".exe" on Windows and empty elsewhere.
	EXESuffix string `json:"exe_suffix"`
	// PathSeparator and PathListSeparator are the values a child process and a
	// PATH-like value use on this platform.
	PathSeparator     string `json:"path_separator"`
	PathListSeparator string `json:"path_list_separator"`
	// CaseInsensitivePaths reports whether two spellings that differ only by
	// case name the same file (Windows: yes, Linux: no).
	CaseInsensitivePaths bool `json:"case_insensitive_paths"`
}

// Current describes the process that is running right now.
func Current() Descriptor { return Describe(runtime.GOOS, runtime.GOARCH) }

// Describe returns the conventions for goos/goarch. An unknown GOOS is treated
// as POSIX: the conservative choice (case-sensitive, no .exe) that matches
// every non-Windows target this project runs on.
func Describe(goos, goarch string) Descriptor {
	if goos == Windows {
		return Descriptor{
			GOOS:                 goos,
			GOARCH:               goarch,
			Family:               "windows",
			EXESuffix:            ".exe",
			PathSeparator:        `\`,
			PathListSeparator:    ";",
			CaseInsensitivePaths: true,
		}
	}
	return Descriptor{
		GOOS:                 goos,
		GOARCH:               goarch,
		Family:               "posix",
		EXESuffix:            "",
		PathSeparator:        "/",
		PathListSeparator:    ":",
		CaseInsensitivePaths: false,
	}
}

// IsWindows reports whether goos names Windows.
func IsWindows(goos string) bool { return goos == Windows }

// TrimExecutableSuffix removes one trailing ".exe", case-insensitively. It does
// not decide whether the caller is on Windows: the suffix can reach us from
// another platform's process table (see ExecutableBase).
func TrimExecutableSuffix(name string) string {
	if len(name) >= 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		return name[:len(name)-4]
	}
	return name
}

// ExecutableName returns the on-disk name a binary has on goos: the same name
// plus ".exe" on Windows, unchanged elsewhere. An existing .exe suffix is not
// doubled.
func ExecutableName(goos, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || !IsWindows(goos) {
		return name
	}
	if len(name) >= 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		return name
	}
	return name + ".exe"
}

// ExecutableCandidates lists the names to probe for one logical binary, in
// priority order. Windows accepts the bare name in a command line because the
// loader appends .exe, so an explicit path without the suffix is still tried
// second rather than rejected.
func ExecutableCandidates(goos, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if !IsWindows(goos) || (len(name) >= 4 && strings.EqualFold(name[len(name)-4:], ".exe")) {
		return []string{name}
	}
	return []string{name + ".exe", name}
}

// ExecutableBase returns the last element of a process's executable name or
// path. Both separators are accepted on every platform on purpose: Windows
// process listings carry `\` paths, POSIX process listings carry `/` paths, and
// tests feed Windows fixtures to a Linux runner. Recognizing both is an
// equivalence, not a platform branch.
func ExecutableBase(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return name
}
