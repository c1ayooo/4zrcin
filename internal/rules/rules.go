// Package rules owns the instance's rule library (docs/20): the files under
// <data>/rules that the node watches, the node's report of what it runs next
// to them, and the two-layer list the console shows. An instance rule covers
// the built-in one with the same name or id; whether a rule is in effect is
// the node's word, read from its report and never assumed here.
package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Kinds of rule file, named like the directories that hold them.
const (
	KindFinger = "finger"
	KindPOC    = "poc"
)

// Origins of a row: what the node started with, or a file of this instance.
const (
	OriginBuiltin  = "builtin"
	OriginInstance = "instance"
)

// NodeDir is where the node writes back (docs/20 §5.2). The console reads it
// and never writes it.
const NodeDir = ".node"

// The files the node writes into NodeDir.
const (
	builtinFingersHTTP   = "builtin-fingers-http.json"
	builtinFingersSocket = "builtin-fingers-socket.json"
	builtinPOCs          = "builtin-pocs.yaml"
	nodeState            = "state.json"
)

var (
	ErrNotFound      = errors.New("规则不存在")
	ErrRevision      = errors.New("这条规则已被别处修改，请刷新后重试")
	ErrNotConfigured = errors.New("规则库未配置")
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Rule is one row of the console's list.
type Rule struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
	// Path is the instance file, relative to the library directory.
	Path   string `json:"path,omitempty"`
	Origin string `json:"origin"`
	// Covers is true when an instance rule takes the place of a built-in one:
	// a fingerprint of the same name, a POC of the same id.
	Covers bool `json:"covers_builtin,omitempty"`
	// Fingers are the fingerprints a POC names in its finger field. The scan
	// pipeline runs a POC only for a target that showed one of them, so a POC
	// with none is left to a direct neutron call.
	Fingers  []string `json:"fingers,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Severity string   `json:"severity,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	// State and Reason are the node's report for an instance file; empty
	// until the node reports.
	State    string `json:"state,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Revision string `json:"revision,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

// ListOptions filters and pages the list. Query matches name, id, path and
// tags, case-insensitively.
type ListOptions struct {
	Kind   string
	Origin string
	Query  string
	Offset int
	Limit  int
}

// NodeFile is one record of the node's state file.
type NodeFile struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Parsed   int    `json:"parsed,omitempty"`
	Compiled int    `json:"compiled,omitempty"`
}

// ImportResult reports what one import did. Skipped entries carry a reason:
// silently dropping a file is how a rule library rots.
type ImportResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	Paths    []string `json:"paths,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

// ContentRef names the rule to read: an instance file by path, a built-in by
// kind and name (a fingerprint's name, a POC's id).
type ContentRef struct {
	Kind   string
	Origin string
	Path   string
	Name   string
}

type pocDoc struct {
	ID      string   `yaml:"id"`
	Fingers []string `yaml:"finger"`
	Info    struct {
		Name     string `yaml:"name"`
		Severity string `yaml:"severity"`
		Tags     any    `yaml:"tags"`
	} `yaml:"info"`
}

type fingerDoc struct {
	Name     string    `yaml:"name"`
	Protocol string    `yaml:"protocol"`
	Tags     []string  `yaml:"tag"`
	Rule     yaml.Node `yaml:"rule"`
}

// Store is the rule library of one instance: its directory, the node's report
// next to it, and the parsed rows in between.
//
// The list is read on every console open and merges thousands of built-in
// rows with the instance's files, so it is parsed once per directory state
// and rebuilt only when a file's name, size or modification time changed.
type Store struct {
	Dir string

	mu          sync.Mutex
	sig         string
	builtin     []Rule
	instance    []Rule
	states      map[string]NodeFile
	builtinIDs  map[string]bool
	builtinName map[string]bool
}

func New(dir string) *Store { return &Store{Dir: dir} }

// List returns one page of the merged library and how many rows the filter
// keeps before paging.
func (s *Store) List(opt ListOptions) ([]Rule, int, error) {
	if err := s.refresh(); err != nil {
		return nil, 0, err
	}
	s.mu.Lock()
	rows := s.merge()
	s.mu.Unlock()

	needle := strings.ToLower(strings.TrimSpace(opt.Query))
	matched := make([]Rule, 0, len(rows))
	for _, row := range rows {
		if opt.Kind != "" && row.Kind != opt.Kind {
			continue
		}
		if opt.Origin != "" && row.Origin != opt.Origin {
			continue
		}
		if needle != "" && !matches(row, needle) {
			continue
		}
		matched = append(matched, row)
	}
	if opt.Offset < 0 {
		opt.Offset = 0
	}
	if opt.Offset > len(matched) {
		opt.Offset = len(matched)
	}
	page := matched[opt.Offset:]
	if opt.Limit > 0 && len(page) > opt.Limit {
		page = page[:opt.Limit]
	}
	return append([]Rule(nil), page...), len(matched), nil
}

// Content returns the rule's text. A built-in fingerprint is rendered as the
// YAML list the node would load, so "save as instance rule" round-trips.
func (s *Store) Content(ref ContentRef) (string, error) {
	if err := s.refresh(); err != nil {
		return "", err
	}
	switch ref.Origin {
	case OriginInstance:
		path, err := CleanPath(ref.Path)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(path)))
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNotFound
		}
		return string(data), err
	case OriginBuiltin:
		return s.builtinContent(ref)
	}
	return "", ErrNotFound
}

// Put writes an instance file. expectRevision is the revision the caller
// read; it must be the file's current revision, and empty for a new file.
// The file is replaced whole and its modification time moves forward, so the
// node never sees a partial write and never misses a same-size rewrite.
func (s *Store) Put(path, content, expectRevision string) ([]Rule, error) {
	if s.Dir == "" {
		return nil, ErrNotConfigured
	}
	path, err := CleanPath(path)
	if err != nil {
		return nil, err
	}
	rows, err := parseInstance(path, []byte(content))
	if err != nil {
		return nil, err
	}
	if err := s.refresh(); err != nil {
		return nil, err
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(path))
	current, exists, err := revisionOf(full)
	if err != nil {
		return nil, err
	}
	switch {
	case exists && expectRevision != current:
		return nil, ErrRevision
	case !exists && expectRevision != "":
		return nil, ErrNotFound
	}
	var prev time.Time
	if info, err := os.Stat(full); err == nil {
		prev = info.ModTime()
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(full, []byte(content)); err != nil {
		return nil, err
	}
	moveForward(full, prev)
	s.invalidate()
	// The write answers with its rows, so they say what they took the place
	// of, the same way the merged list does.
	s.mu.Lock()
	for i, row := range rows {
		if (row.Kind == KindPOC && s.builtinIDs[row.ID]) || (row.Kind == KindFinger && s.builtinName[row.Name]) {
			rows[i].Covers = true
		}
	}
	s.mu.Unlock()
	return rows, nil
}

// Delete removes an instance file. The revision must be the file's current
// one, so a console working from a stale list cannot delete a rewritten rule.
func (s *Store) Delete(path, expectRevision string) error {
	if s.Dir == "" {
		return ErrNotConfigured
	}
	path, err := CleanPath(path)
	if err != nil {
		return err
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(path))
	current, exists, err := revisionOf(full)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if expectRevision == "" || expectRevision != current {
		return ErrRevision
	}
	if err := os.Remove(full); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// Import copies rule files into the library. A file with a top-level id is a
// POC and lands under pocs/<id>.yaml; a list of fingerprints lands under
// fingers/<category>/, the category being the file's directory inside the
// source when it has one. Anything else is skipped with a reason.
func (s *Store) Import(src string) (ImportResult, error) {
	var res ImportResult
	if s.Dir == "" {
		return res, ErrNotConfigured
	}
	info, err := os.Stat(src)
	if err != nil {
		return res, err
	}
	if !info.IsDir() {
		s.importOne(src, "", &res)
		return res, nil
	}
	err = filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !isYAML(entry.Name()) || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		category, _ := filepath.Rel(src, filepath.Dir(path))
		s.importOne(path, category, &res)
		return nil
	})
	return res, err
}

// Status returns the node's state file as it was written, and whether the
// node reported at all. A node that is down or predates the brick leaves no
// file, and the console says so instead of showing a guess.
func (s *Store) Status() (map[string]any, bool, error) {
	if s.Dir == "" {
		return nil, false, ErrNotConfigured
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, NodeDir, nodeState))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false, fmt.Errorf("节点状态文件无法解析：%w", err)
	}
	return state, true, nil
}

func (s *Store) importOne(src, category string, res *ImportResult) {
	data, err := os.ReadFile(src)
	if err != nil {
		res.skip(src, err.Error())
		return
	}
	dest, err := classify(filepath.Base(src), data, category)
	if err != nil {
		res.skip(src, err.Error())
		return
	}
	if err := s.refresh(); err != nil {
		res.skip(src, err.Error())
		return
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(dest))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		res.skip(src, err.Error())
		return
	}
	var prev time.Time
	if info, err := os.Stat(full); err == nil {
		prev = info.ModTime()
	}
	if err := writeFileAtomic(full, data); err != nil {
		res.skip(src, err.Error())
		return
	}
	moveForward(full, prev)
	res.Imported++
	res.Paths = append(res.Paths, dest)
}

func (res *ImportResult) skip(src, reason string) {
	res.Skipped++
	res.Errors = append(res.Errors, filepath.Base(src)+"："+reason)
}

// classify says where an imported file belongs.
func classify(name string, data []byte, category string) (string, error) {
	if doc, err := parsePOC(data); err == nil && idPattern.MatchString(doc.ID) {
		return "pocs/" + doc.ID + ".yaml", nil
	}
	if fingers, err := parseFingers(data); err == nil && len(fingers) > 0 {
		if category == "" || category == "." {
			category = "custom"
		}
		return "fingers/" + filepath.ToSlash(category) + "/" + name, nil
	}
	return "", errors.New("既不是带 id 的 POC，也不是指纹列表")
}

// refresh rebuilds the parsed rows when a file under the directory changed.
func (s *Store) refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Dir == "" {
		return ErrNotConfigured
	}
	sig, err := signature(s.Dir)
	if err != nil {
		return err
	}
	if sig == s.sig {
		return nil
	}
	builtin, err := readBuiltins(filepath.Join(s.Dir, NodeDir))
	if err != nil {
		return err
	}
	states, err := readNodeState(filepath.Join(s.Dir, NodeDir, nodeState))
	if err != nil {
		return err
	}
	instance, err := readInstance(s.Dir)
	if err != nil {
		return err
	}
	s.sig, s.builtin, s.instance, s.states = sig, builtin, instance, states
	s.builtinIDs, s.builtinName = make(map[string]bool), make(map[string]bool)
	for _, row := range builtin {
		if row.Kind == KindPOC {
			s.builtinIDs[row.ID] = true
		} else {
			s.builtinName[row.Name] = true
		}
	}
	return nil
}

func (s *Store) invalidate() {
	s.mu.Lock()
	s.sig = ""
	s.mu.Unlock()
}

// merge joins the two layers for the console. An instance fingerprint covers
// the built-in one of the same name, an instance POC the built-in one of the
// same id. A state record for a file that is gone is still a row, so the
// operator sees what the node last said about it.
func (s *Store) merge() []Rule {
	rows := make([]Rule, 0, len(s.instance)+len(s.builtin)+len(s.states))
	coveredNames := make(map[string]bool)
	coveredIDs := make(map[string]bool)
	listed := make(map[string]bool, len(s.instance))
	for _, row := range s.instance {
		listed[row.Path] = true
		if state, ok := s.states[row.Path]; ok {
			row.State, row.Reason = state.State, state.Reason
		}
		switch row.Kind {
		case KindFinger:
			if s.builtinName[row.Name] {
				row.Covers = true
				coveredNames[row.Name] = true
			}
		case KindPOC:
			if s.builtinIDs[row.ID] {
				row.Covers = true
				coveredIDs[row.ID] = true
			}
		}
		rows = append(rows, row)
	}
	for path, state := range s.states {
		if listed[path] {
			continue
		}
		rows = append(rows, Rule{
			Kind: kindOf(path), Name: filepath.Base(path), Path: path, Origin: OriginInstance,
			State: state.State, Reason: state.Reason,
		})
	}
	for _, row := range s.builtin {
		if row.Kind == KindFinger && coveredNames[row.Name] {
			continue
		}
		if row.Kind == KindPOC && coveredIDs[row.ID] {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}

func (s *Store) builtinContent(ref ContentRef) (string, error) {
	node := filepath.Join(s.Dir, NodeDir)
	if ref.Kind == KindPOC {
		data, err := os.ReadFile(filepath.Join(node, builtinPOCs))
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNotFound
		}
		if err != nil {
			return "", err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil {
			return "", fmt.Errorf("解析 %s: %w", builtinPOCs, err)
		}
		if len(document.Content) == 0 || document.Content[0].Kind != yaml.SequenceNode {
			return "", ErrNotFound
		}
		for _, item := range document.Content[0].Content {
			var doc pocDoc
			if err := item.Decode(&doc); err != nil {
				continue
			}
			if doc.ID == ref.Name {
				out, err := yaml.Marshal(item)
				return string(out), err
			}
		}
		return "", ErrNotFound
	}
	for _, name := range []string{builtinFingersHTTP, builtinFingersSocket} {
		data, err := os.ReadFile(filepath.Join(node, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		var fingers []map[string]any
		if len(data) == 0 || json.Unmarshal(data, &fingers) != nil {
			continue
		}
		for _, finger := range fingers {
			if finger["name"] == ref.Name {
				// The SDK's JSON spells the embedded attributes as a field
				// name. An empty one is noise to the operator and to the node
				// that reads a saved copy back.
				if empty, ok := finger["Attributes"].(map[string]any); ok && len(empty) == 0 {
					delete(finger, "Attributes")
				}
				out, err := yaml.Marshal([]any{finger})
				return string(out), err
			}
		}
	}
	return "", ErrNotFound
}

// CleanPath validates an instance file's path: local, inside fingers/ or
// pocs/, with a yaml extension and a name every platform accepts.
func CleanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", errors.New("路径无效：必须是规则目录内的相对路径")
	}
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	depth := strings.Count(path, "/")
	switch kindOf(path) {
	case KindPOC:
		if depth != 1 {
			return "", errors.New("POC 文件直接放在 pocs/ 目录下")
		}
	case KindFinger:
		if depth != 2 {
			return "", errors.New("指纹文件放在 fingers/<分类>/ 目录下")
		}
	default:
		return "", errors.New("路径必须以 fingers/ 或 pocs/ 开头")
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext != ".yaml" && ext != ".yml" {
		return "", errors.New("规则文件的扩展名必须是 .yaml 或 .yml")
	}
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ".") || strings.ContainsAny(segment, `<>:"|?*`) || strings.TrimSpace(segment) != segment {
			return "", errors.New("路径里有不能用的名称：" + segment)
		}
	}
	return path, nil
}

// parseInstance reads the rows of one instance file and rejects what the node
// would reject anyway: a POC without a usable id, a fingerprint file that is
// not a list of fingerprints with a name and a rule. Whether it compiles is
// the node's verdict and comes back in its report.
func parseInstance(path string, data []byte) ([]Rule, error) {
	info := Rule{Kind: kindOf(path), Path: path, Origin: OriginInstance, Size: int64(len(data)), Revision: revision(data)}
	switch info.Kind {
	case KindPOC:
		doc, err := parsePOC(data)
		if err != nil {
			return nil, err
		}
		if !idPattern.MatchString(doc.ID) {
			return nil, errors.New("POC 缺少合法的 id（只允许字母、数字和 . _ -）")
		}
		info.ID, info.Severity, info.Tags, info.Fingers = doc.ID, doc.Info.Severity, splitTags(doc.Info.Tags), doc.Fingers
		info.Name = doc.Info.Name
		if info.Name == "" {
			info.Name = doc.ID
		}
		return []Rule{info}, nil
	case KindFinger:
		fingers, err := parseFingers(data)
		if err != nil {
			return nil, err
		}
		if len(fingers) == 0 {
			return nil, errors.New("指纹文件必须是一个非空列表")
		}
		rows := make([]Rule, 0, len(fingers))
		for i, finger := range fingers {
			if finger.Name == "" || finger.Rule.Kind == 0 {
				return nil, fmt.Errorf("第 %d 条指纹缺少 name 或 rule", i+1)
			}
			protocol := finger.Protocol
			if protocol == "" {
				protocol = "http"
			}
			rows = append(rows, Rule{Kind: KindFinger, Name: finger.Name, Tags: finger.Tags, Protocol: protocol,
				Path: path, Origin: OriginInstance, Size: info.Size, Revision: info.Revision})
		}
		return rows, nil
	}
	return nil, ErrNotFound
}

func parsePOC(data []byte) (pocDoc, error) {
	var doc pocDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("不是合法的 YAML：%v", err)
	}
	return doc, nil
}

func parseFingers(data []byte) ([]fingerDoc, error) {
	var fingers []fingerDoc
	if err := yaml.Unmarshal(data, &fingers); err != nil {
		return nil, fmt.Errorf("不是合法的指纹列表：%v", err)
	}
	return fingers, nil
}

// readInstance lists the instance files. A file that does not parse is still
// a row, named by its file, so a hand-edited rule never disappears.
func readInstance(dir string) ([]Rule, error) {
	var rows []Rule
	for _, sub := range []string{"fingers", "pocs"} {
		err := filepath.WalkDir(filepath.Join(dir, sub), func(path string, entry fs.DirEntry, err error) error {
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return nil
			case err != nil:
				return err
			case entry.IsDir() || !isYAML(entry.Name()) || strings.HasPrefix(entry.Name(), "."):
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			parsed, err := parseInstance(rel, data)
			if err != nil {
				parsed = []Rule{{Kind: kindOf(rel), Name: filepath.Base(rel), Path: rel, Origin: OriginInstance,
					Size: int64(len(data)), Revision: revision(data)}}
			}
			rows = append(rows, parsed...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// readBuiltins lists the rules the node started with, from the copies it
// wrote next to the instance files.
func readBuiltins(dir string) ([]Rule, error) {
	var rows []Rule
	for _, name := range []string{builtinFingersHTTP, builtinFingersSocket} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var fingers []struct {
			Name     string   `json:"name"`
			Protocol string   `json:"protocol"`
			Tags     []string `json:"tag"`
		}
		if err := json.Unmarshal(data, &fingers); err != nil {
			return nil, fmt.Errorf("解析 %s: %w", name, err)
		}
		for _, finger := range fingers {
			rows = append(rows, Rule{Kind: KindFinger, Name: finger.Name, Protocol: finger.Protocol, Tags: finger.Tags, Origin: OriginBuiltin})
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, builtinPOCs))
	if errors.Is(err, fs.ErrNotExist) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", builtinPOCs, err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s 不是模板列表", builtinPOCs)
	}
	for _, item := range document.Content[0].Content {
		var doc pocDoc
		if err := item.Decode(&doc); err != nil {
			continue
		}
		name := doc.Info.Name
		if name == "" {
			name = doc.ID
		}
		rows = append(rows, Rule{Kind: KindPOC, Name: name, ID: doc.ID, Severity: doc.Info.Severity, Tags: splitTags(doc.Info.Tags), Fingers: doc.Fingers, Origin: OriginBuiltin})
	}
	return rows, nil
}

func readNodeState(path string) (map[string]NodeFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]NodeFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state struct {
		Files []NodeFile `json:"files"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("节点状态文件无法解析：%w", err)
	}
	files := make(map[string]NodeFile, len(state.Files))
	for _, file := range state.Files {
		files[file.Path] = file
	}
	return files, nil
}

// signature lists every file under the directory with its size and
// modification time.
func signature(dir string) (string, error) {
	var b strings.Builder
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s\x00%d\x00%d\n", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return b.String(), err
}

func revisionOf(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return revision(data), true, nil
}

func revision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func kindOf(path string) string {
	switch {
	case strings.HasPrefix(path, "fingers/"):
		return KindFinger
	case strings.HasPrefix(path, "pocs/"):
		return KindPOC
	}
	return ""
}

func isYAML(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

func matches(rule Rule, needle string) bool {
	return strings.Contains(strings.ToLower(rule.Name), needle) ||
		strings.Contains(strings.ToLower(rule.ID), needle) ||
		strings.Contains(strings.ToLower(rule.Path), needle) ||
		strings.Contains(strings.ToLower(strings.Join(rule.Tags, " ")), needle)
}

// splitTags reads a POC's tags, which nuclei writes either as a comma
// separated string or as a list.
func splitTags(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case string:
		raw = strings.Split(typed, ",")
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				raw = append(raw, text)
			}
		}
	}
	var tags []string
	for _, tag := range raw {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func moveForward(path string, prev time.Time) {
	info, err := os.Stat(path)
	if err != nil || info.ModTime().After(prev) {
		return
	}
	_ = os.Chtimes(path, prev.Add(time.Second), prev.Add(time.Second))
}

// writeFileAtomic replaces a file whole: the node reads the old content or
// the new one, never a partial write.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// DerivePath names a new file for a pasted rule: a POC by its id, a list of
// fingerprints by its first fingerprint's name, under the given category.
func DerivePath(content, category string) (string, error) {
	if doc, err := parsePOC([]byte(content)); err == nil && idPattern.MatchString(doc.ID) {
		return "pocs/" + doc.ID + ".yaml", nil
	}
	fingers, err := parseFingers([]byte(content))
	if err != nil || len(fingers) == 0 || fingers[0].Name == "" {
		return "", errors.New("既不是带 id 的 POC，也不是带 name 的指纹列表")
	}
	if category = strings.TrimSpace(category); category == "" {
		category = "custom"
	}
	return "fingers/" + category + "/" + slug(fingers[0].Name) + ".yaml", nil
}

// slug turns a fingerprint's name into a file name every platform accepts; a
// name with nothing usable in it falls back to its digest.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "rule-" + revision([]byte(name))
	}
	return b.String()
}

// AdoptLegacyPOCs brings the POC library this package replaced into the rule
// library, once: only when the library has no POC of its own and the old
// directory has files. The old files stay where they were; nothing is moved
// or rewritten behind the operator's back.
func AdoptLegacyPOCs(old, dir string) (int, error) {
	entries, err := os.ReadDir(old)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && isYAML(entry.Name()) && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return 0, nil
	}
	dest := filepath.Join(dir, "pocs")
	taken, err := os.ReadDir(dest)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if len(taken) > 0 {
		return 0, nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}
	copied := 0
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(old, name))
		if err != nil {
			return copied, err
		}
		if err := writeFileAtomic(filepath.Join(dest, name), data); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}
