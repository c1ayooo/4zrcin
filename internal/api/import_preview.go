package api

// The import preview (spec #1 工单8/#9): scan results and HAR captures are
// judged row by row against the project fence BEFORE anything lands. The
// preview writes nothing — no ledger row, no rule, no asset — so the operator
// can open it, close it, and open it again with the same answer.
//
// Three groups, and only three:
//
//	in_fence     a rule row admits the host (its action may still be stricter)
//	out_of_fence the table admits nothing: default-miss, i.e. outside the fence
//	unjudged     the project has no enabled fence, so the question cannot be
//	             answered — never reported as "outside"
//
// Confirming an import with selected out-of-fence hosts writes those hosts
// into the fence (one allow row each, the asset in_fence) and then runs the
// import unchanged. No selection = the pre-existing behavior, byte for byte.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/assets"
	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/project"
	"github.com/c1ayooo/4zrcin/internal/traffic"
)

// ImportPreviewItem is one imported host and how the fence reads it.
type ImportPreviewItem struct {
	Host     string `json:"host"`
	Kind     string `json:"kind"`
	Ports    int    `json:"ports,omitempty"`
	Requests int    `json:"requests,omitempty"`
	IP       string `json:"ip,omitempty"`
	// Classification is in_fence | out_of_fence | unjudged.
	Classification string `json:"classification"`
	Action         string `json:"action,omitempty"`
	RuleID         string `json:"rule_id,omitempty"`
	RuleText       string `json:"rule_text,omitempty"`
}

// ImportPreview is one parsed import, judged, with nothing written.
type ImportPreview struct {
	Kind         string              `json:"kind"` // assets | traffic
	Path         string              `json:"path"`
	Records      int                 `json:"records"`
	Hosts        int                 `json:"hosts"`
	InFence      int                 `json:"in_fence"`
	OutOfFence   int                 `json:"out_of_fence"`
	Unjudged     int                 `json:"unjudged"`
	FenceEnabled bool                `json:"fence_enabled"`
	Truncated    int                 `json:"truncated,omitempty"`
	Items        []ImportPreviewItem `json:"items"`
}

func resolveCasePath(caseRoot, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(caseRoot, filepath.FromSlash(path))
}

// fenceEnabled says whether the preview can judge at all.
func (s *Server) fenceEnabled() bool {
	if s.Project == nil {
		return false
	}
	rules, ok := s.Project.ScopeRulesOf()
	return ok && rules.Enabled
}

// classifyHost is the one place the preview turns a rule table into a group.
func (s *Server) classifyHost(host string) (classification, action, ruleID, ruleText string) {
	if s.Project == nil {
		return "unjudged", "", "", ""
	}
	rules, ok := s.Project.ScopeRulesOf()
	if !ok {
		return "unjudged", "", "", ""
	}
	verdict := rules.JudgeHost(host)
	if !verdict.Enabled {
		return "unjudged", "", "", ""
	}
	if !verdict.Matched {
		return "out_of_fence", "", "", ""
	}
	for _, rule := range rules.Rules {
		if rule.ID == verdict.RuleID {
			return "in_fence", verdict.Action, verdict.RuleID, rule.Text()
		}
	}
	return "in_fence", verdict.Action, verdict.RuleID, ""
}

func (p *ImportPreview) count(item ImportPreviewItem) {
	switch item.Classification {
	case "in_fence":
		p.InFence++
	case "out_of_fence":
		p.OutOfFence++
	default:
		p.Unjudged++
	}
}

// assetImportPreview parses a harness result stream and judges one item per
// host address (the identity the importer will write).
func (s *Server) assetImportPreview(path string) (ImportPreview, error) {
	resolved := resolveCasePath(s.CaseRoot, path)
	records, err := assets.ParseJSONL(resolved)
	if err != nil {
		return ImportPreview{}, err
	}
	out := ImportPreview{Kind: "assets", Path: resolved, Records: len(records)}
	ports := map[string]int{}
	order := []string{}
	for _, r := range records {
		ip := strings.ToLower(strings.TrimSpace(r.IP))
		if ip == "" {
			continue
		}
		if _, ok := ports[ip]; !ok {
			order = append(order, ip)
		}
		ports[ip]++
	}
	sort.Strings(order)
	if len(order) > assets.MaxHosts {
		out.Truncated = len(order) - assets.MaxHosts
		order = order[:assets.MaxHosts]
	}
	for _, ip := range order {
		item := ImportPreviewItem{Host: ip, Kind: model.AssetKindHost, Ports: ports[ip]}
		item.Classification, item.Action, item.RuleID, item.RuleText = s.classifyHost(ip)
		out.Items = append(out.Items, item)
	}
	out.Hosts = len(out.Items)
	for _, item := range out.Items {
		out.count(item)
	}
	out.FenceEnabled = s.fenceEnabled()
	return out, nil
}

// trafficImportPreview parses one HAR capture and judges one item per hostname
// (the fence is host-based; the capture's ports stay in the count).
func (s *Server) trafficImportPreview(path string) (ImportPreview, error) {
	resolved := resolveCasePath(s.CaseRoot, path)
	har, err := traffic.Parse(resolved)
	if err != nil {
		return ImportPreview{}, err
	}
	hosts := traffic.Summarise(har)
	base := ImportPreview{Kind: "traffic", Path: resolved, Records: len(har.Log.Entries)}
	if len(hosts) > traffic.MaxHosts {
		base.Truncated = len(hosts) - traffic.MaxHosts
		hosts = hosts[:traffic.MaxHosts]
	}
	return s.finishTrafficPreview(base, hosts), nil
}

func (s *Server) finishTrafficPreview(out ImportPreview, hosts []traffic.Host) ImportPreview {
	requests := map[string]int{}
	ports := map[string]int{}
	ips := map[string]string{}
	order := []string{}
	for _, h := range hosts {
		name := h.Key
		if host, _, err := net.SplitHostPort(h.Key); err == nil {
			name = host
		}
		name = strings.ToLower(strings.Trim(name, "[]"))
		if name == "" {
			continue
		}
		if _, ok := requests[name]; !ok {
			order = append(order, name)
		}
		requests[name] += h.Requests
		ports[name]++
		if h.IP != "" && ips[name] == "" {
			ips[name] = h.IP
		}
	}
	sort.Strings(order)
	for _, name := range order {
		item := ImportPreviewItem{Host: name, Kind: model.AssetKindDomain}
		if model.IsIPLiteral(name) {
			item.Kind = model.AssetKindHost
		}
		item.Requests, item.Ports, item.IP = requests[name], ports[name], ips[name]
		item.Classification, item.Action, item.RuleID, item.RuleText = s.classifyHost(name)
		out.Items = append(out.Items, item)
	}
	out.Hosts = len(out.Items)
	for _, item := range out.Items {
		out.count(item)
	}
	out.FenceEnabled = s.fenceEnabled()
	return out
}

// POST /api/assets/import/preview
func (s *Server) previewAssetImport(w http.ResponseWriter, r *http.Request) {
	path, ok := importPath(w, r, "扫描结果文件（相对案例目录）")
	if !ok {
		return
	}
	preview, err := s.assetImportPreview(path)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, preview)
}

// POST /api/traffic/import/preview
func (s *Server) previewTrafficImport(w http.ResponseWriter, r *http.Request) {
	path, ok := importPath(w, r, "HAR 文件在服务器上的位置")
	if !ok {
		return
	}
	preview, err := s.trafficImportPreview(path)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, preview)
}

func importPath(w http.ResponseWriter, r *http.Request, hint string) (string, bool) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json: "+err.Error())
		return "", false
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		writeErr(w, 400, "需要 path："+hint)
		return "", false
	}
	return req.Path, true
}

// applyFenceSelection validates the operator's checked hosts against a fresh
// preview (only genuinely out-of-fence hosts may be added — a typo must not
// widen the fence) and then writes the allow rows and the assets.
func (s *Server) applyFenceSelection(preview ImportPreview, selected []string) (int, error) {
	if len(selected) == 0 {
		return 0, nil
	}
	allowed := map[string]bool{}
	for _, item := range preview.Items {
		if item.Classification == "out_of_fence" {
			allowed[item.Host] = true
		}
	}
	if len(allowed) == 0 {
		return 0, errors.New("这次导入没有超出围栏的主机；没有可加入的范围项")
	}
	hosts := make([]string, 0, len(selected))
	for _, raw := range selected {
		host, err := project.NormalizeFenceHost(raw)
		if err != nil {
			return 0, fmt.Errorf("主机 %q 不是合法的围栏项：%w", raw, err)
		}
		if !allowed[host] {
			return 0, fmt.Errorf("%s 不在这次预览的超出围栏列表里，不能加入围栏", host)
		}
		hosts = append(hosts, host)
	}
	return s.addHostsToFence(hosts)
}

// applyImportFence is the shared confirm step of both import endpoints. It
// returns (rows added, ok); a failed write answers the request itself.
func (s *Server) applyImportFence(
	w http.ResponseWriter,
	path string,
	selected []string,
	previewFn func(string) (ImportPreview, error),
) (int, bool) {
	if len(selected) == 0 {
		return 0, true
	}
	preview, err := previewFn(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return 0, false
	}
	added, err := s.applyFenceSelection(preview, selected)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return 0, false
	}
	return added, true
}

// writeImportResult keeps the importer's own receipt fields and adds the fence
// count only when a selection was made, so a no-selection import is byte-for-
// byte the legacy response.
func writeImportResult(w http.ResponseWriter, res any, fenceAdded int) {
	raw, err := json.Marshal(res)
	if err != nil {
		writeJSON(w, http.StatusOK, res)
		return
	}
	payload := map[string]any{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeJSON(w, http.StatusOK, res)
		return
	}
	if fenceAdded > 0 {
		payload["fence_added"] = fenceAdded
	}
	writeJSON(w, http.StatusOK, payload)
}

// addHostsToFence is the batch membership write: one allow row per host ahead
// of the table (first-hit-wins), and the asset in_fence. It never changes the
// fallback action — the fence's own fixed default stays.
func (s *Server) addHostsToFence(hosts []string) (int, error) {
	if s.Project == nil {
		return 0, errors.New("围栏不可用：项目未挂载")
	}
	rules, ok := s.Project.ScopeRulesOf()
	if !ok || !rules.Enabled {
		return 0, errors.New("项目未启用围栏：无法把导入主机加入范围")
	}
	if len(hosts) > project.MaxScopeRules {
		return 0, fmt.Errorf("一次最多加入 %d 个主机", project.MaxScopeRules)
	}
	seen := map[string]bool{}
	var fresh []project.ScopeRule
	for _, raw := range hosts {
		host, err := project.NormalizeFenceHost(raw)
		if err != nil {
			return 0, err
		}
		if host == "" || host == "*" || seen[host] {
			continue
		}
		seen[host] = true
		if hasAllowRow(rules, host) {
			continue
		}
		fresh = append(fresh, project.ScopeRule{
			Action: project.ScopeActionRecord, Method: "*", Host: host, PathPrefix: "/",
		})
	}
	if len(fresh) > 0 {
		rules.Rules = append(fresh, rules.Rules...)
		if err := s.persistScopeRules(&rules); err != nil {
			return 0, err
		}
	}
	// A checked host with an existing pending/denied row is admitted through
	// the approval tail (guidance revoked, continue intent, commander told) —
	// checking the box IS the approval, so the models must hear it like any
	// other join. A fresh host lands as in_fence with no announcement: the
	// import receipt already names the count.
	var admits []model.Asset
	for host := range seen {
		kind := model.AssetKindDomain
		if model.IsIPLiteral(host) {
			kind = model.AssetKindHost
		}
		if existing, found := s.Store.AssetByKindKey(kind, host); found {
			if existing.Fence == model.AssetFenceIn {
				continue
			}
			admits = append(admits, existing)
			continue
		}
		if _, _, err := s.Store.UpsertAsset(model.Asset{
			Kind: kind, Key: host, Fence: model.AssetFenceIn, Source: model.AssetSourceFence,
		}); err != nil {
			return len(fresh), err
		}
	}
	if len(admits) > 0 {
		ctx, cancel := fenceNotifyCtx()
		defer cancel()
		for _, asset := range admits {
			if _, err := s.admitAsset(ctx, asset); err != nil {
				return len(fresh), err
			}
		}
	}
	return len(fresh), nil
}
