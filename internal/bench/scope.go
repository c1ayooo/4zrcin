package bench

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// ScopeText renders the project's scope document (起点) for one moment of a
// measured run. It is the single place that decides what "we may touch this"
// means, because the document it writes is the only thing the commander and the
// executors read as the allowed target set.
//
// Three rules, each learned from a way this can go wrong:
//
//   - The set is the union of every available container's addresses, sorted and
//     de-duplicated — not the answer of the last `start`. With three containers
//     alive, the two older ones are still legitimate targets, and a scope that
//     names only the newest would quietly forbid the work in flight.
//   - An empty set is an error and writes nothing. Writing "no addresses" would
//     read to a model as "no target is allowed" and to a careless reader as "no
//     restriction"; either way the run continues under a scope that is a lie.
//     The honest answer is to stop and say the platform reports nothing usable.
//   - A malformed address is an error too. Dropping it would silently shrink the
//     scope, and the model would be blamed for not reaching a target we removed.
func ScopeText(addrs []string) (string, error) {
	clean := make([]string, 0, len(addrs))
	seen := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(a); err != nil {
			return "", fmt.Errorf("bench: platform returned an address that is not host:port: %q", a)
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		clean = append(clean, a)
	}
	if len(clean) == 0 {
		// Not an empty document: no document at all, plus a failure the caller
		// must surface. See the rule above.
		return "", fmt.Errorf("bench: no available container address; refusing to write an empty scope")
	}
	sort.Strings(clean)

	var b strings.Builder
	b.WriteString("本任务当前允许触及的地址（取自平台返回的 available 容器，是本次唯一允许触及的目标）：\n")
	for _, a := range clean {
		b.WriteString("- " + a + "\n")
	}
	b.WriteString("\n容器关闭后这些地址可能被分配给其他题目，不能长期绑定；以每次刷新的本清单为准。")
	return b.String(), nil
}
