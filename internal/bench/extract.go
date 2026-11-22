package bench

import (
	"net"
	"regexp"
	"strings"
)

// DefaultFlagPattern is the shape the run looks for unless the operator passes a
// different one. It is a default, not a claim about the platform: if a challenge
// prints its flags in another shape, the run submits nothing and says so (see
// ExtractFlags) instead of quietly reporting "no flags found".
const DefaultFlagPattern = `flag\{[^{}]{1,512}\}`

// ExtractFlags returns the distinct flag-looking strings in text, in first-seen
// order. An empty result means "no flag in this text", which the caller must
// report rather than treat as success.
func ExtractFlags(text string, re *regexp.Regexp) []string {
	if re == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range re.FindAllString(text, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// ResolveChallenge maps one fact's entity to the challenge that owns it: the
// entity is the address the executor worked on, and the current snapshot knows
// which challenge that address belongs to. Matching on the host alone covers the
// common "host:port" versus "host" difference; a miss is a miss, because
// guessing would submit a flag against the wrong challenge.
func ResolveChallenge(entityRef string, active []Challenge) (string, bool) {
	entityRef = strings.TrimSpace(entityRef)
	if entityRef == "" {
		return "", false
	}
	host := entityRef
	if h, _, err := net.SplitHostPort(entityRef); err == nil {
		host = h
	}
	for _, ch := range active {
		for _, a := range ch.ContainerAddr {
			if a == entityRef {
				return ch.UniqueCode, true
			}
			if h, _, err := net.SplitHostPort(a); err == nil && h == host {
				return ch.UniqueCode, true
			}
		}
	}
	return "", false
}
