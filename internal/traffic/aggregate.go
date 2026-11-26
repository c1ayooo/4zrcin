package traffic

// The asset page's traffic side (spec #1 工单6/#7): one pass over the capture
// index, aggregated per host, with the per-URL rows the drill-down shows.
// The index is append-only and immutable per file, so the aggregate caches by
// the index signature (file count and newest mtime) — a page poll does not
// re-read a hundred thousand flows, and a newly captured flow invalidates the
// cache by its own file.

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HostTraffic is one observed host's capture aggregate.
type HostTraffic struct {
	Host string `json:"host"`
	// URLs and Requests count unique path shapes and observed requests.
	URLs     int `json:"urls"`
	Requests int `json:"requests"`
	// Statuses counts response buckets ("2xx"/"4xx"/"error") — the table
	// shows a compact shape, not every code.
	Statuses map[string]int `json:"statuses"`
	// Intents are the intent ids whose runs produced these flows,
	// de-duplicated: "which directions touched this host".
	Intents []string `json:"intents"`
	// LastSeen is the newest flow time on this host.
	LastSeen time.Time `json:"last_seen"`
}

// URLTraffic is one path shape observed on a host.
type URLTraffic struct {
	URL      string         `json:"url"`
	Requests int            `json:"requests"`
	Statuses map[string]int `json:"statuses"`
	Intents  []string       `json:"intents"`
	LastSeen time.Time      `json:"last_seen"`
	// The newest exchange's summary: status, transport error, response bytes.
	LastStatus int    `json:"last_status,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	LastBytes  int    `json:"last_bytes,omitempty"`
	// Verdict is the newest scope-rule verdict carried by any of these flows
	// ("record"/"review"/"block"), chosen by flow time — the index is read in
	// hash-name order, so "last read" is not "last observed".
	Verdict string `json:"verdict,omitempty"`
	RuleID  string `json:"rule_id,omitempty"`
	Missed  bool   `json:"default_missed,omitempty"`
	// RuleText is the matched rule's own text, filled by the control plane
	// from the project manifest (the traffic layer has no manifest).
	RuleText string `json:"rule_text,omitempty"`
}

// HostAggregator caches the whole-index aggregate by directory signature.
type HostAggregator struct {
	mu         sync.Mutex
	sig        string
	byHost     map[string]*HostTraffic
	order      []string
	urlsByHost map[string][]URLTraffic
}

// Hosts returns the aggregate map and its recency order for one case root,
// re-reading the index only when its signature changed.
func (a *HostAggregator) Hosts(cases string) (map[string]*HostTraffic, []string) {
	if a == nil {
		return nil, nil
	}
	sig := indexSignature(cases)
	a.mu.Lock()
	if a.sig == sig && a.byHost != nil {
		byHost, order := a.byHost, a.order
		a.mu.Unlock()
		return byHost, order
	}
	a.mu.Unlock()
	// The index read happens outside the lock: a page poll for another row
	// must not serialize behind this walk. A concurrent read for the same
	// signature may store its aggregation after ours; both describe the same
	// files, so either is correct.
	byHost, order, urls := aggregateIndex(cases)
	a.mu.Lock()
	a.sig, a.byHost, a.order, a.urlsByHost = sig, byHost, order, urls
	a.mu.Unlock()
	return byHost, order
}

// URLs returns the per-URL rows for one host (exact hostname match).
func (a *HostAggregator) URLs(cases, host string) []URLTraffic {
	a.Hosts(cases)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.urlsByHost[strings.ToLower(host)]
}

// indexSignature is the cache key: file count plus the newest modification
// time in .http. Files are written once and never rewritten, so an unchanged
// count with an older newest-mtime cannot hide an edit.
func indexSignature(cases string) string {
	entries, err := os.ReadDir(filepath.Join(cases, ".http"))
	if err != nil {
		return "none"
	}
	count := 0
	var newest int64
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		count++
		if info, err := e.Info(); err == nil && info.ModTime().UnixNano() > newest {
			newest = info.ModTime().UnixNano()
		}
	}
	return strconv.Itoa(count) + ":" + strconv.FormatInt(newest, 10)
}

func aggregateIndex(cases string) (map[string]*HostTraffic, []string, map[string][]URLTraffic) {
	byHost := map[string]*HostTraffic{}
	var order []string
	urlRows := map[string]*URLTraffic{} // host\x00path → row

	entries, err := os.ReadDir(filepath.Join(cases, ".http"))
	if err != nil {
		return byHost, nil, nil
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cases, ".http", e.Name()))
		if err != nil {
			continue
		}
		var r Capture
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		parsed, err := url.Parse(r.URL)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		bucket := statusBucket(int(r.Status), r)
		h := byHost[host]
		if h == nil {
			h = &HostTraffic{Host: host, Statuses: map[string]int{}}
			byHost[host] = h
			order = append(order, host)
		}
		h.Requests++
		h.Statuses[bucket]++
		if h.LastSeen.Before(r.At) {
			h.LastSeen = r.At
		}
		if r.IntentID != "" && !contains(h.Intents, r.IntentID) {
			h.Intents = append(h.Intents, r.IntentID)
		}
		key := host + "\x00" + parsed.Path
		row := urlRows[key]
		if row == nil {
			row = &URLTraffic{Statuses: map[string]int{}}
			urlRows[key] = row
			h.URLs++
		}
		row.Requests++
		row.Statuses[bucket]++
		if row.LastSeen.Before(r.At) || row.URL == "" {
			row.LastSeen = r.At
			row.URL = r.URL
			row.LastStatus = int(r.Status)
			row.LastError = r.Error
			row.LastBytes = r.ResponseBytes
		}
		if r.IntentID != "" && !contains(row.Intents, r.IntentID) {
			row.Intents = append(row.Intents, r.IntentID)
		}
		// Newest verdict by flow time; a flow without a verdict never erases
		// one observed earlier.
		if r.GuardrailAction != "" {
			if row.Verdict == "" || !r.At.Before(row.LastSeen) {
				row.Verdict, row.RuleID, row.Missed = r.GuardrailAction, r.GuardrailRuleID, r.GuardrailDefaultMissed
			}
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return byHost[order[i]].LastSeen.After(byHost[order[j]].LastSeen) })
	urlsByHost := map[string][]URLTraffic{}
	for key, row := range urlRows {
		host := key[:strings.IndexByte(key, 0)]
		rows := urlsByHost[host]
		rows = append(rows, *row)
		urlsByHost[host] = rows
	}
	for host := range urlsByHost {
		rows := urlsByHost[host]
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Requests != rows[j].Requests {
				return rows[i].Requests > rows[j].Requests
			}
			return rows[i].URL < rows[j].URL
		})
		urlsByHost[host] = rows
	}
	return byHost, order, urlsByHost
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// statusBucket compresses a flow into the table's status shape: a bucket per
// class, "error" for a captured transport failure, "none" for a request
// without a response.
func statusBucket(status int, r Capture) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	case status == 0 && r.Error != "":
		return "error"
	default:
		return "none"
	}
}
