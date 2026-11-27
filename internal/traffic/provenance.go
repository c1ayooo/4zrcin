package traffic

// The provenance of a fence-pending asset (spec #1 待审审批): where the host
// came from, assembled from records the project already keeps — the refused
// exchanges in the .http index and the passive subresource records. Nothing
// here actively touches the network except one bounded DNS lookup per host,
// cached; the fence is a statement about the target, the control plane does
// not probe unapproved hosts.

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// HostProvenance is the decision context for one pending host: what the fence
// refused on its behalf, what the target itself pulled from it, and how the
// name resolves relative to the operator's own range.
type HostProvenance struct {
	Host string `json:"host"`
	// RefusedCount is every fence refusal (any action) whose URL named this
	// host, newest-window bounded (see provenanceMaxFiles). The URL list and
	// times are the decision meat: one probe vs persistent hammering.
	RefusedCount   int       `json:"refused_count,omitempty"`
	FirstRefusedAt time.Time `json:"first_refused_at,omitempty"`
	LastRefusedAt  time.Time `json:"last_refused_at,omitempty"`
	RefusedURLs    []string  `json:"refused_urls,omitempty"`
	RunIDs         []string  `json:"run_ids,omitempty"`
	IntentIDs      []string  `json:"intent_ids,omitempty"`
	// PassiveCount is what the target itself pulled from this host (page
	// references, fork patch 34): a "belongs to the target's ecosystem"
	// signal, recorded not judged.
	PassiveCount int `json:"passive_count,omitempty"`
	// ProbeNote marks name shapes that are probe/junk candidates (a bare
	// single label, or an IP literal spliced into a domain — the canonical
	// out-of-band test spelling). Empty for an ordinary hostname.
	ProbeNote string `json:"probe_note,omitempty"`
	// ResolvedIPs is the control plane's view of the host's addresses
	// (cached, bounded). It may differ from what the scan node resolves —
	// different network position — and is labelled 仅供参考 on the console.
	ResolvedIPs []string `json:"resolved_ips,omitempty"`
	// SameOrigin lists in-range hosts that resolve to an address this host
	// also resolves to (exact or same IPv4 /24): "another name for a server
	// we already allow" is the strongest approve signal there is.
	SameOrigin []string `json:"same_origin,omitempty"`
}

const (
	provenanceMaxFiles   = 4000
	provenanceMaxURLs    = 5
	provenanceMaxRunIDs  = 3
	provenanceMaxIntent  = 3
	provenanceMaxSame    = 4
	provenanceMaxIPs     = 4
	provenanceResolveTO  = 2 * time.Second
	provenanceResTTL     = 5 * time.Minute
	provenanceResEntries = 256
)

// provenanceURLHost extracts and normalizes the host a capture named: lower,
// trailing dot dropped (the fence matcher treats the spelling as separate —
// provenance groups it so one host's attempts read together).
func provenanceURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.Trim(parsed.Hostname(), "."))
	return host
}

// probeNoteFor classifies name shapes that read as probe/junk candidates.
// A single label is not a resolvable public domain; an IP literal spliced as
// the first label of a longer name is the canonical out-of-band test spelling.
// Everything else stays unmarked — guessing beyond the shape would mislabel
// real hosts.
func probeNoteFor(host string) string {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
	if h == "" {
		return ""
	}
	if !strings.Contains(h, ".") {
		return "单标签主机名，不是可解析的公网域名"
	}
	// An IPv4 literal spliced as the first four labels of a longer name
	// ("114.111.27.202.evil.com") is the canonical out-of-band test spelling.
	labels := strings.Split(h, ".")
	if len(labels) >= 5 {
		if _, err := netip.ParseAddr(strings.Join(labels[:4], ".")); err == nil {
			return "IP 前缀拼接域名，疑似带外探测"
		}
	}
	return ""
}

// resolveProvenanceHosts resolves the given hosts (bounded, cached). Lookup
// failures and empty answers are cached like successes: an unresolvable name
// must not turn every provenance read into a fresh timeout.
var (
	provResMu     sync.Mutex
	provResCache  = map[string]provResEntry{}
	provResRing   []string
	provResLookup = func(ctx context.Context, host string) ([]net.IP, error) {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
		return ips, nil
	}
	provResCounter int
)

type provResEntry struct {
	ips     []string
	expires time.Time
}

// resetProvenanceResolution clears the resolution cache. Test seam: the cache
// is package-global and outlives a single test.
func resetProvenanceResolution() {
	provResMu.Lock()
	defer provResMu.Unlock()
	provResCache = map[string]provResEntry{}
	provResRing = nil
}

func resolveProvenanceHosts(hosts []string) map[string][]string {
	now := time.Now()
	out := make(map[string][]string, len(hosts))
	provResMu.Lock()
	defer provResMu.Unlock()
	for _, host := range hosts {
		host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
		if host == "" {
			continue
		}
		if e, ok := provResCache[host]; ok && now.Before(e.expires) {
			if len(e.ips) > 0 {
				out[host] = e.ips
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), provenanceResolveTO)
		addrs, err := provResLookup(ctx, host)
		cancel()
		var ips []string
		if err == nil {
			seen := map[string]bool{}
			for _, a := range addrs {
				s := a.String()
				if !seen[s] {
					seen[s] = true
					ips = append(ips, s)
				}
			}
		}
		provResCache[host] = provResEntry{ips: ips, expires: now.Add(provenanceResTTL)}
		provResRing = append(provResRing, host)
		if len(provResRing) > provenanceResEntries {
			delete(provResCache, provResRing[0])
			provResRing = provResRing[1:]
		}
		provResCounter++
		_ = provResCounter
		if len(ips) > 0 {
			out[host] = ips
		}
	}
	return out
}

func provIPFamiliesOverlap(a, b []string) (exact bool, subnet bool) {
	for _, x := range a {
		xa, err := netip.ParseAddr(x)
		if err != nil {
			continue
		}
		for _, y := range b {
			ya, err := netip.ParseAddr(y)
			if err != nil {
				continue
			}
			if xa == ya {
				exact = true
			}
			if xa.Is4() && ya.Is4() {
				x4 := xa.Unmap().As4()
				y4 := ya.Unmap().As4()
				if netip.PrefixFrom(netip.AddrFrom4(x4), 24).Contains(netip.AddrFrom4(y4)) {
					subnet = true
				}
			}
		}
	}
	return exact, subnet
}

// ProvenanceFor assembles HostProvenance for the named hosts from the case's
// capture index, in one bounded pass (newest files win — the same reading
// choice the audit and traffic views make). SameOrigin compares each host's
// resolved addresses against the inRange hosts' (both resolved through the
// same cached resolver). Hosts with no records return no entry.
func ProvenanceFor(cases string, hosts []string, inRangeHosts []string) map[string]HostProvenance {
	want := map[string]bool{}
	for _, h := range hosts {
		h = provenanceURLHost("https://" + h + "/")
		if h != "" {
			want[h] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	dir := filepath.Join(cases, ".http")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	// Newest files first: provenance is about the recent attempts, and a
	// project with a huge index must not make every read scan the whole tree.
	sort.Slice(entries, func(i, j int) bool {
		ii, e1 := entries[i].Info()
		ij, e2 := entries[j].Info()
		if e1 != nil || e2 != nil {
			return entries[i].Name() > entries[j].Name()
		}
		return ii.ModTime().After(ij.ModTime())
	})
	type acc struct {
		count  int
		first  time.Time
		last   time.Time
		urls   []string
		runs   []string
		intens []string
		passiv int
	}
	accu := map[string]*acc{}
	read := 0
	for _, entry := range entries {
		if read >= provenanceMaxFiles {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		read++
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var c Capture
		if json.Unmarshal(raw, &c) != nil || c.URL == "" {
			continue
		}
		host := provenanceURLHost(c.URL)
		if !want[host] {
			continue
		}
		a := accu[host]
		if a == nil {
			a = &acc{}
			accu[host] = a
		}
		if c.GuardrailPassive {
			a.passiv++
			continue
		}
		if c.GuardrailAction == "" {
			continue
		}
		a.count++
		if a.first.IsZero() || c.At.Before(a.first) {
			a.first = c.At
		}
		if c.At.After(a.last) {
			a.last = c.At
		}
		if len(a.urls) < provenanceMaxURLs && !slices.Contains(a.urls, c.URL) {
			a.urls = append(a.urls, c.URL)
		}
		if c.RunID != "" && len(a.runs) < provenanceMaxRunIDs && !slices.Contains(a.runs, c.RunID) {
			a.runs = append(a.runs, c.RunID)
		}
		if c.IntentID != "" && len(a.intens) < provenanceMaxIntent && !slices.Contains(a.intens, c.IntentID) {
			a.intens = append(a.intens, c.IntentID)
		}
	}
	if len(accu) == 0 {
		return nil
	}
	out := make(map[string]HostProvenance, len(accu))
	for host, a := range accu {
		p := HostProvenance{
			Host:           host,
			RefusedCount:   a.count,
			FirstRefusedAt: a.first,
			LastRefusedAt:  a.last,
			RefusedURLs:    a.urls,
			RunIDs:         a.runs,
			IntentIDs:      a.intens,
			PassiveCount:   a.passiv,
			ProbeNote:      probeNoteFor(host),
		}
		out[host] = p
	}
	// Same-origin: resolve the pending hosts and the operator's own range,
	// then compare. Purely additive — resolution failures leave it unset.
	if len(inRangeHosts) > 0 {
		pending := make([]string, 0, len(out))
		for host := range out {
			pending = append(pending, host)
		}
		resolved := resolveProvenanceHosts(append(pending, inRangeHosts...))
		inRangeIPs := map[string][]string{}
		var order []string
		for _, host := range inRangeHosts {
			host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "."))
			if host == "" {
				continue
			}
			if ips := resolved[host]; len(ips) > 0 {
				if _, seen := inRangeIPs[host]; !seen {
					order = append(order, host)
				}
				inRangeIPs[host] = ips
			}
		}
		for host, p := range out {
			ips := resolved[host]
			if len(ips) == 0 {
				continue
			}
			for _, inHost := range order {
				if inHost == host {
					continue
				}
				exact, subnet := provIPFamiliesOverlap(ips, inRangeIPs[inHost])
				if exact || subnet {
					tag := inHost
					if subnet && !exact {
						tag = inHost + "（同网段）"
					}
					if len(p.SameOrigin) < provenanceMaxSame && !slices.Contains(p.SameOrigin, tag) {
						p.SameOrigin = append(p.SameOrigin, tag)
					}
				}
			}
			// p is a copy of the map value: write the flagged copy back.
			if len(p.SameOrigin) > 0 {
				p.ResolvedIPs = ips
				if len(p.ResolvedIPs) > provenanceMaxIPs {
					p.ResolvedIPs = p.ResolvedIPs[:provenanceMaxIPs]
				}
				out[host] = p
			}
			if len(p.SameOrigin) > 0 {
				p.ResolvedIPs = ips
				if len(p.ResolvedIPs) > provenanceMaxIPs {
					p.ResolvedIPs = p.ResolvedIPs[:provenanceMaxIPs]
				}
				out[host] = p
			}
		}
	}
	return out
}
