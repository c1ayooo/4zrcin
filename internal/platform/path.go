package platform

import (
	"path"
	"strings"
)

// NormalizePath folds one path (or one path-shaped field of a command line)
// into the canonical lexical form of goos. It does not touch the filesystem: it
// never resolves symlinks, checks existence or expands drive mappings, because
// process tables and command lines from another platform must be comparable
// without those facilities.
//
// Windows: both separators are accepted and folded to `\`, "." and ".." are
// resolved lexically, a drive prefix (C:) and UNC prefix (\\server\share) are
// preserved, and ".." never climbs above the root. Linux/POSIX: only '/' is a
// separator; a backslash is an ordinary file-name byte, and path.Clean is the
// canonical form.
func NormalizePath(goos, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !IsWindows(goos) {
		return path.Clean(p)
	}
	return normalizeWindowsPath(p)
}

// PathKey is NormalizePath plus the platform's case folding, so two keys can be
// compared for identity. Windows paths are case-insensitive (drive letter
// included); POSIX paths are not.
func PathKey(goos, p string) string {
	n := NormalizePath(goos, p)
	if IsWindows(goos) {
		return strings.ToLower(n)
	}
	return n
}

// SamePath reports whether a and b name the same lexical path on goos. Empty
// values never match: a missing path is not the empty path.
func SamePath(goos, a, b string) bool {
	ka, kb := PathKey(goos, a), PathKey(goos, b)
	return ka != "" && kb != "" && ka == kb
}

// IsAbsolute reports whether p is anchored at the platform's root. On Windows a
// drive-relative path ("C:foo") and a bare name are not absolute; on POSIX only
// a leading "/" is.
func IsAbsolute(goos, p string) bool {
	p = strings.TrimSpace(p)
	if p == "" {
		return false
	}
	if !IsWindows(goos) {
		return strings.HasPrefix(p, "/")
	}
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// SplitList splits a PATH-like value with the separator goos uses.
func SplitList(goos, value string) []string {
	if value == "" {
		return nil
	}
	sep := ":"
	if IsWindows(goos) {
		sep = ";"
	}
	return strings.Split(value, sep)
}

// IsPathBoundary lists the bytes that end a path inside a command line:
// separators, quotes, flags and whitespace. It is platform-independent on
// purpose: process command lines carry either separator on either platform.
func IsPathBoundary(b byte) bool {
	switch b {
	case '\\', '/', '"', '\'', '=', ' ', '\t', '\r', '\n', ';', ',':
		return true
	}
	return false
}

// ContainsPathSegment reports whether haystack names needle as a complete path,
// not as a prefix of a longer name: "<data>/data-ab" is not a mention of
// "<data>/data-a". Both separators and the platform's case rule are folded
// first, so a Windows command line and a POSIX command line can be checked with
// the same predicate. A needle that normalizes to "" or "." matches nothing:
// the instance data directory must be a real path.
func ContainsPathSegment(goos, haystack, needle string) bool {
	n := PathKey(goos, needle)
	if n == "" || n == "." {
		return false
	}
	hay := PathKey(goos, haystack)
	offset := 0
	for {
		i := strings.Index(hay[offset:], n)
		if i < 0 {
			return false
		}
		start := offset + i
		end := start + len(n)
		if (start == 0 || IsPathBoundary(hay[start-1])) &&
			(end == len(hay) || IsPathBoundary(hay[end])) {
			return true
		}
		offset = start + 1
	}
}

// normalizeWindowsPath is NormalizePath for GOOS=windows. It is a lexical
// cleaner only; it deliberately does not call filepath.Clean, which on a
// non-Windows host would keep `\` as an ordinary byte and make the Windows and
// Linux rules untestable from the same test.
func normalizeWindowsPath(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	prefix := ""
	rooted := false
	rest := p
	switch {
	case strings.HasPrefix(p, `\\`):
		// UNC: \\server\share is the root; ".." cannot climb above it.
		parts := strings.SplitN(p[2:], `\`, 3)
		if len(parts) >= 2 {
			prefix = `\\` + parts[0] + `\` + parts[1]
			if len(parts) == 3 {
				rest = parts[2]
			} else {
				rest = ""
			}
		} else {
			prefix, rest = `\\`, p[2:]
		}
		rooted = true
	case len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':':
		prefix, rest = p[:2], p[2:]
		if strings.HasPrefix(rest, `\`) {
			rooted = true
			rest = rest[1:]
		}
	case strings.HasPrefix(p, `\`):
		prefix, rooted, rest = `\`, true, p[1:]
	}
	var out []string
	for _, seg := range strings.Split(rest, `\`) {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			} else if !rooted {
				out = append(out, "..")
			}
			// An absolute path stays at its root; "C:\.." is "C:\".
		default:
			out = append(out, seg)
		}
	}
	joined := strings.Join(out, `\`)
	switch {
	case prefix == `\`:
		return `\` + joined
	case prefix != "" && rooted:
		if joined == "" {
			// The root itself keeps its anchor: "C:" is drive-relative, "C:\"
			// is not, so an absolute input must not normalize into the former.
			if strings.HasPrefix(prefix, `\\`) {
				return prefix
			}
			return prefix + `\`
		}
		return prefix + `\` + joined
	case prefix != "":
		// Drive-relative: "C:" + relative tail keeps its no-separator form.
		return prefix + joined
	default:
		if joined == "" {
			return "."
		}
		return joined
	}
}

func isDriveLetter(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}
