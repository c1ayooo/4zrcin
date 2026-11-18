package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kksty/tianwu/internal/platform"
)

// ToolStat is one row of the tool-call leaderboard. Calls are counted from the
// run logs (kind=tool_call), not from facts: a call that produced nothing still
// cost a turn, and that is exactly what the ranking is for.
type ToolStat struct {
	Tool       string     `json:"tool"`
	Calls      int        `json:"calls"`
	Runs       int        `json:"runs"`
	LastAt     *time.Time `json:"last_at,omitempty"`
	Local      bool       `json:"local,omitempty"`
	Completed  int        `json:"completed"`
	Failures   int        `json:"failures"`
	DurationMS int64      `json:"duration_ms,omitempty"`
}

// ArsenalTool is one binary found in an arsenal bin directory, with the
// version/managed_by fields from that directory's manifest.json when present.
// Dirs lists every arsenal root that holds the same binary: an operator may
// have installed it both instance-wide and for the local user, and the page
// should say so once, not twice.
type ArsenalTool struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Size        int64    `json:"size,omitempty"`
	Source      string   `json:"source,omitempty"`
	ManagedBy   string   `json:"managed_by,omitempty"`
	InstalledAt string   `json:"installed_at,omitempty"`
	Dirs        []string `json:"dirs,omitempty"`
}

// toolRoot is one arsenal root the local harness scan node may resolve tool
// binaries from. The root holds bin/ (the executables) and manifest.json (what
// arsenal installed); the executable directory is what the runtime prepends to
// the model's PATH (harness cmd/cyber-scan/extensions.go:37-41).
//
// A root is a location, not a claim that a tool is usable: "文件存在不等于可用"
// (agent.md). Whether the host can actually run commands is reported by the
// runtime catalogue in the same response, never inferred from this list.
type toolRoot struct {
	Path   string `json:"path"`
	Bin    string `json:"bin"`
	Source string `json:"source"` // instance | harness | user | configured
	Exists bool   `json:"exists"` // <path>/bin exists
	// Resolved marks the root the running scan resolves by the harness's own
	// data-directory precedence. Only one root is marked when it can be derived.
	Resolved bool `json:"resolved,omitempty"`
	// PathInjected marks the directory the runtime prepends to the child PATH.
	PathInjected bool   `json:"path_injected,omitempty"`
	Note         string `json:"note,omitempty"`
}

type toolsResponse struct {
	Leaderboard []ToolStat    `json:"leaderboard"`
	RunsScanned int           `json:"runs_scanned"`
	Arsenal     []ArsenalTool `json:"arsenal"`
	ArsenalDirs []string      `json:"arsenal_dirs,omitempty"`
	// ToolRoots lists every candidate root with its source, so the console can
	// show where the harness will actually find tools instead of one guessed
	// directory. The historical arsenal/arsenal_dirs fields are kept verbatim.
	ToolRoots []toolRoot `json:"tool_roots,omitempty"`
	// ToolRootsNote explains how the roots were derived and what cannot be
	// observed from the runtime, so a missing root is explainable.
	ToolRootsNote string `json:"tool_roots_note,omitempty"`
	// Runtime is the live host catalogue (commands/model/health). A file that
	// exists but is not declared by the running harness is not promoted to
	// "available": the state and error here are the availability answer.
	Runtime *runtimeToolsResponse `json:"runtime,omitempty"`
}

// toolFile is one run log's contribution, cached until the file changes.
type toolFile struct {
	size      int64
	mod       time.Time
	calls     map[string]int
	last      map[string]time.Time
	completed map[string]int
	failures  map[string]int
	duration  map[string]int64
}

func (s *Server) listTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "只支持 GET"})
		return
	}
	roots := s.toolRoots()
	arsenal, dirs := s.arsenalTools(roots)
	rows, scanned := s.toolLeaderboard(arsenal)
	// Availability is the running host's declaration, not a filesystem guess: a
	// dead host reports state/error instead of an empty "successful" catalogue.
	runtime := s.runtimeCatalogue(r.Context(), runtimeCatalogueTimeout)
	writeJSON(w, http.StatusOK, toolsResponse{
		Leaderboard:   rows,
		RunsScanned:   scanned,
		Arsenal:       arsenal,
		ArsenalDirs:   dirs,
		ToolRoots:     roots,
		ToolRootsNote: toolRootsNote,
		Runtime:       &runtime,
	})
}

// toolLeaderboard aggregates tool_call events over every run log of the
// project. A run log is re-read only when its size or mtime changed, so the
// page stays cheap after the first load even with large JSONL files.
//
// Callers that already listed the arsenal pass it in: the same request used to
// scan every tool root twice (once here, once for the response) on a page the
// console polls every ten seconds.
func (s *Server) toolLeaderboard(scannedArsenal ...[]ArsenalTool) ([]ToolStat, int) {
	dir := filepath.Join(s.DataDir, "runs")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}

	type tally struct {
		calls               int
		runs                int
		last                time.Time
		completed, failures int
		duration            int64
	}
	var arsenal []ArsenalTool
	if len(scannedArsenal) > 0 {
		arsenal = scannedArsenal[0]
	} else {
		arsenal, _ = s.arsenalTools(s.toolRoots())
	}
	localNames := map[string]bool{}
	for _, name := range strings.Fields("gogo curl katana spray playwright proton neutron passive zombie scan loop mitm fetch rg python python3 node") {
		localNames[name] = true
	}
	for _, tool := range arsenal {
		localNames[strings.ToLower(tool.Name)] = true
	}
	byTool := map[string]*tally{}
	scanned := 0

	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		scanned++

		s.mu.Lock()
		cached, ok := s.toolCache[path]
		s.mu.Unlock()
		if !ok || cached.size != st.Size() || !cached.mod.Equal(st.ModTime()) {
			cached = readToolCalls(path, st)
			s.mu.Lock()
			s.toolCache[path] = cached
			s.mu.Unlock()
		}

		for tool, n := range cached.calls {
			if strings.HasPrefix(tool, "local:") && !localNames[strings.TrimPrefix(tool, "local:")] {
				continue
			}
			t := byTool[tool]
			if t == nil {
				t = &tally{}
				byTool[tool] = t
			}
			t.calls += n
			t.completed += cached.completed[tool]
			t.failures += cached.failures[tool]
			t.duration += cached.duration[tool]
			t.runs++
			if at := cached.last[tool]; at.After(t.last) {
				t.last = at
			}
		}
	}

	out := make([]ToolStat, 0, len(byTool))
	for tool, t := range byTool {
		row := ToolStat{Tool: tool, Calls: t.calls, Runs: t.runs}
		row.Completed, row.Failures, row.DurationMS = t.completed, t.failures, t.duration
		if strings.HasPrefix(tool, "local:") {
			row.Tool = strings.TrimPrefix(tool, "local:")
			row.Local = true
		}
		if !t.last.IsZero() {
			at := t.last
			row.LastAt = &at
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Tool < out[j].Tool
	})
	return out, scanned
}

// readToolCalls reads one run log and keeps only what the leaderboard needs.
func readToolCalls(path string, st os.FileInfo) toolFile {
	out := toolFile{
		size:      st.Size(),
		mod:       st.ModTime(),
		calls:     map[string]int{},
		last:      map[string]time.Time{},
		completed: map[string]int{}, failures: map[string]int{}, duration: map[string]int64{},
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 1<<20)
	toolsByCall := map[string]string{}
	for {
		line, readErr := reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			var ev struct {
				Kind       string          `json:"kind"`
				Tool       string          `json:"tool"`
				At         time.Time       `json:"at"`
				CallID     string          `json:"call_id"`
				OK         bool            `json:"ok"`
				DurationMS int64           `json:"duration_ms"`
				Raw        json.RawMessage `json:"raw"`
			}
			if json.Unmarshal([]byte(trimmed), &ev) == nil && ev.Kind == "tool_call" && ev.Tool != "" {
				out.calls[ev.Tool]++
				if ev.CallID != "" {
					toolsByCall[ev.CallID] = ev.Tool
				}
				if ev.Tool == "bash" {
					commands := shellCommands(toolCommand(ev.Raw))
					for _, command := range commands {
						key := "local:" + command
						out.calls[key]++
						if ev.At.After(out.last[key]) {
							out.last[key] = ev.At
						}
					}
				}
				if ev.At.After(out.last[ev.Tool]) {
					out.last[ev.Tool] = ev.At
				}
			}
			if ev.Kind == "tool_result" {
				tool := ev.Tool
				if tool == "" {
					tool = toolsByCall[ev.CallID]
				}
				delete(toolsByCall, ev.CallID)
				if tool != "" {
					out.completed[tool]++
					if !ev.OK {
						out.failures[tool]++
					}
					out.duration[tool] += ev.DurationMS
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	return out
}

// toolRoots is the candidate set the console reports. An explicit override
// (tests, alternate deployments) replaces the derived roots; otherwise the
// roots come from the harness's own data-directory rule, so the page names the
// directory the running scan actually reads instead of a guessed one.
func (s *Server) toolRoots() []toolRoot {
	if len(s.ArsenalDirs) > 0 {
		out := make([]toolRoot, 0, len(s.ArsenalDirs))
		for _, root := range s.ArsenalDirs {
			out = append(out, newToolRoot(root, "configured"))
		}
		return out
	}
	return defaultToolRoots(s.DataDir)
}

func newToolRoot(root, source string) toolRoot {
	root = filepath.Clean(root)
	out := toolRoot{Path: root, Bin: filepath.Join(root, "bin"), Source: source}
	if st, err := os.Stat(out.Bin); err == nil && st.IsDir() {
		out.Exists = true
	}
	return out
}

// toolRootsNote is operator-facing provenance, not tool usage: it says which
// resolution rule produced the roots and what the console cannot infer.
const toolRootsNote = "根目录按 harness 数据目录解析规则推导（scan 工作目录下的 .cyber 优先，其次用户 ~/.cyber）；harness 配置里的显式 misc.data_dir 与 scan 可执行文件旁的 .cyber 无法从运行时读取，如有设置以实际宿主为准。"

// instanceDataDir turns the project directory into the instance root. In
// production DataDir is <data>/projects/<id>; a bare server (tests, alternate
// launchers) passes its own root, and then that root is the instance.
func instanceDataDir(projectDir string) string {
	clean := filepath.Clean(projectDir)
	if filepath.Base(filepath.Dir(clean)) == "projects" {
		return filepath.Dir(filepath.Dir(clean))
	}
	return clean
}

// defaultToolRoots derives the roots from the harness's data-directory rule
// (cyber-harness pkg/config/datadir.go:9-26):
//
//	scan workdir/.cyber, then <scan executable dir>/.cyber, then ~/.cyber
//
// tianwu launches the scan with its working directory set to the instance data
// directory (internal/host/host.go execLaunch), so the first candidate is
// <instance>/.cyber and the last is the user directory. The scan executable
// directory is not observable from the AOP runtime, so it is not guessed; the
// note says so. The legacy instance root (<instance>/arsenal) is kept and
// labelled, because an operator may still drop tools there by hand.
func defaultToolRoots(projectDir string) []toolRoot {
	instance := instanceDataDir(projectDir)
	roots := []toolRoot{newToolRoot(filepath.Join(instance, "arsenal"), "instance")}

	harnessData := filepath.Join(instance, ".cyber")
	harness := newToolRoot(filepath.Join(harnessData, "arsenal"), "harness")
	if st, err := os.Stat(harnessData); err == nil && st.IsDir() {
		harness.Resolved = true
		harness.PathInjected = true
	} else {
		harness.Note = "scan 工作目录下还没有 .cyber；harness 当前回退到用户目录"
	}
	roots = append(roots, harness)

	if home, err := os.UserHomeDir(); err == nil {
		user := newToolRoot(filepath.Join(home, ".cyber", "arsenal"), "user")
		// The user root is the harness fallback only while <instance>/.cyber is
		// absent; once it exists the scan never reads the user arsenal.
		user.Resolved = !harness.Resolved
		user.PathInjected = user.Resolved
		if harness.Resolved {
			user.Note = "harness 已优先使用 scan 工作目录下的 .cyber，不再读取该目录"
		}
		roots = append(roots, user)
	}
	return roots
}

// arsenalTools lists what arsenal installed on this machine, per candidate root.
// The list is an inventory: the same response carries the runtime catalogue, and
// a file that exists but is not declared by the running host is not called
// "available" here.
func (s *Server) arsenalTools(roots []toolRoot) ([]ArsenalTool, []string) {
	seen := map[string]bool{}
	byName := map[string][]ArsenalTool{}
	var dirs []string
	for _, root := range roots {
		bin := root.Bin
		st, err := os.Stat(bin)
		if err != nil || !st.IsDir() || seen[bin] {
			continue
		}
		seen[bin] = true
		dirs = append(dirs, bin)

		manifest := readArsenalManifest(filepath.Join(root.Path, "manifest.json"))
		ents, err := os.ReadDir(bin)
		if err != nil {
			continue
		}
		for _, ent := range ents {
			name := ent.Name()
			if ent.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			name = platform.TrimExecutableSuffix(name)
			info, err := ent.Info()
			if err != nil {
				continue
			}
			row := ArsenalTool{Name: name, Size: info.Size(), Dirs: []string{bin}}
			if m, ok := manifest[name]; ok {
				row.Version = m.Version
				row.Source = m.Source
				row.ManagedBy = m.ManagedBy
				row.InstalledAt = m.InstalledAt
			}
			byName[name] = append(byName[name], row)
		}
	}

	out := make([]ArsenalTool, 0, len(byName))
	for _, rows := range byName {
		pick := rows[0]
		var where []string
		for _, row := range rows {
			where = append(where, row.Dirs[0])
			switch {
			case pick.Version == "" && row.Version != "":
				pick = row // prefer the copy the manifest describes
			case row.Version == pick.Version && row.Size > pick.Size:
				pick = row // a stale 1-byte placeholder must not be the headline
			}
		}
		// Show the directory of the copy the page is describing first.
		for i, dir := range where {
			if dir == pick.Dirs[0] {
				where[0], where[i] = where[i], where[0]
				break
			}
		}
		pick.Dirs = where
		out = append(out, pick)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, dirs
}

type arsenalManifestEntry struct {
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at"`
	Source      string `json:"source"`
	ManagedBy   string `json:"managed_by"`
}

func readArsenalManifest(path string) map[string]arsenalManifestEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]arsenalManifestEntry
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}
