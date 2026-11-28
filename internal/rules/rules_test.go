package rules

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	builtinFingers = `[{"name":"nginx","protocol":"http","tag":["server","web"],"link":"","Attributes":{},"rule":[{"regexps":{"header":["nginx"]}}]},{"name":"mysql","protocol":"tcp","tag":["database"],"default_port":["mysql"]}]`
	builtinPOCYAML = "- id: builtin-poc\n  info:\n    name: Builtin POC\n    severity: high\n    tags: cve,example\n  http:\n  - method: GET\n    path:\n      - \"{{BaseURL}}/\"\n"
	pocYAML        = "id: brick-poc\ninfo:\n  name: Brick POC\n  severity: info\n  tags: [a, b]\nhttp:\n  - method: GET\n    path:\n      - \"{{BaseURL}}/\"\n"
	pocSameSize    = "id: brick-poc\ninfo:\n  name: Other POC\n  severity: info\n  tags: [a, b]\nhttp:\n  - method: GET\n    path:\n      - \"{{BaseURL}}/\"\n"
	fingerYAML     = "- name: brick-finger\n  rule:\n    - regexps:\n        body:\n          - marker\n"
	nodeStateJSON  = `{"brick":1,"available":true,"builtin":{"fingers":2,"pocs":1},"files":[{"path":"pocs/brick-poc.yaml","kind":"poc","state":"applied","parsed":1,"compiled":1},{"path":"fingers/custom/gone.yaml","kind":"finger","state":"unsupported","reason":"the node still runs this file; it is gone after a node restart"}]}`
)

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// library is a store whose node reported two built-in fingerprints and one
// built-in POC.
func library(t *testing.T) *Store {
	t.Helper()
	node := filepath.Join(t.TempDir(), NodeDir)
	write(t, filepath.Join(node, builtinFingersHTTP), builtinFingers)
	write(t, filepath.Join(node, builtinFingersSocket), "[]")
	write(t, filepath.Join(node, builtinPOCs), builtinPOCYAML)
	return New(filepath.Dir(node))
}

func byName(rows []Rule, name string) Rule {
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	return Rule{}
}

func TestListStartsWithTheBuiltins(t *testing.T) {
	s := library(t)
	rows, matched, err := s.List(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 3 || len(rows) != 3 {
		t.Fatalf("rows = %d matched = %d, want 3: %+v", len(rows), matched, rows)
	}
	// Fingers sort before POCs, each group by name.
	want := []string{"mysql", "nginx", "Builtin POC"}
	for i, name := range want {
		if rows[i].Name != name || rows[i].Origin != OriginBuiltin {
			t.Fatalf("row %d = %+v, want built-in %q", i, rows[i], name)
		}
	}
	if nginx := byName(rows, "nginx"); len(nginx.Tags) != 2 || nginx.Protocol != "http" {
		t.Fatalf("nginx = %+v", nginx)
	}
	if poc := byName(rows, "Builtin POC"); poc.ID != "builtin-poc" || poc.Severity != "high" || strings.Join(poc.Tags, ",") != "cve,example" {
		t.Fatalf("built-in POC = %+v", poc)
	}
}

// An instance fingerprint covers the built-in one of the same name; an
// instance POC sits next to the built-ins. The node's report gives each
// instance file its state, and a file the node still runs after it was
// deleted stays listed.
func TestListMergesTheTwoLayersAndTheNodeReport(t *testing.T) {
	s := library(t)
	if _, err := s.Put("fingers/custom/nginx.yaml", fingerYAML2("nginx"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("pocs/brick-poc.yaml", pocYAML, ""); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(s.Dir, NodeDir, nodeState), nodeStateJSON)

	rows, matched, err := s.List(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 5 {
		t.Fatalf("matched = %d, want 5: %+v", matched, rows)
	}
	nginx := byName(rows, "nginx")
	if nginx.Origin != OriginInstance || !nginx.Covers || nginx.Path != "fingers/custom/nginx.yaml" {
		t.Fatalf("nginx = %+v, want the instance file covering the built-in", nginx)
	}
	if row := byName(rows, "Brick POC"); row.Origin != OriginInstance || row.State != "applied" || row.Revision == "" {
		t.Fatalf("instance POC = %+v", row)
	}
	gone := byName(rows, "gone.yaml")
	if gone.Path != "fingers/custom/gone.yaml" || gone.State != "unsupported" || gone.Reason == "" {
		t.Fatalf("removed file = %+v, want listed with the node's reason", gone)
	}
	if byName(rows, "Builtin POC").Kind != KindPOC {
		t.Fatalf("the built-in POC is gone: %+v", rows)
	}
}

func fingerYAML2(name string) string {
	return "- name: " + name + "\n  rule:\n    - regexps:\n        body:\n          - marker\n"
}

func TestListFiltersAndPages(t *testing.T) {
	s := library(t)
	if _, err := s.Put("pocs/brick-poc.yaml", pocYAML, ""); err != nil {
		t.Fatal(err)
	}
	rows, matched, err := s.List(ListOptions{Kind: KindPOC, Offset: 1, Limit: 5})
	if err != nil || matched != 2 || len(rows) != 1 || rows[0].Name != "Builtin POC" {
		t.Fatalf("POCs = %+v matched %d err %v", rows, matched, err)
	}
	if rows, matched, _ = s.List(ListOptions{Origin: OriginInstance}); matched != 1 || rows[0].Path != "pocs/brick-poc.yaml" {
		t.Fatalf("instance rows = %+v matched %d", rows, matched)
	}
	if _, matched, _ = s.List(ListOptions{Query: "DATAbase"}); matched != 1 {
		t.Fatalf("query by tag matched %d, want 1", matched)
	}
	if _, matched, _ = s.List(ListOptions{Query: "brick-poc.yaml"}); matched != 1 {
		t.Fatalf("query by path matched %d, want 1", matched)
	}
}

func TestCleanPathAcceptsWhatTheLibraryLaysOut(t *testing.T) {
	for _, path := range []string{"fingers/custom/a.yaml", "pocs/a.yml"} {
		if got, err := CleanPath(path); err != nil || got != path {
			t.Fatalf("CleanPath(%q) = %q, %v", path, got, err)
		}
	}
}

func TestCleanPathRejectsEverythingElse(t *testing.T) {
	for _, path := range []string{"", "/pocs/a.yaml", "../pocs/a.yaml", "other/a.yaml", "pocs/a.txt", "pocs/sub/a.yaml",
		"fingers/a.yaml", "pocs/.hidden.yaml", "pocs/a<b>.yaml", "fingers//x.yaml"} {
		if _, err := CleanPath(path); err == nil {
			t.Errorf("CleanPath(%q) accepted", path)
		}
	}
}

func TestPutRejectsWhatTheNodeWouldReject(t *testing.T) {
	cases := map[string]string{
		"pocs/a.yaml":           "info:\n  name: no id here\n",
		"fingers/custom/a.yaml": "- name: only-a-name\n",
		"fingers/custom/b.yaml": "- rule:\n    - regexps:\n        body:\n          - x\n",
		"fingers/custom/c.yaml": "name: not-a-list\n",
		"fingers/custom/d.yaml": "- name: ok\n  rule:\n    - regexps:\n        body:\n          - x\n",
	}
	// The last entry is valid; the first four are not.
	for path, body := range cases {
		_, err := library(t).Put(path, body, "")
		if path == "fingers/custom/d.yaml" {
			if err != nil {
				t.Errorf("Put(%s) = %v, want accepted", path, err)
			}
		} else if err == nil {
			t.Errorf("Put(%s) accepted", path)
		}
	}
}

// pocAny is a POC with the given id and name.
func pocAny(id, name string) string {
	return "id: " + id + "\ninfo:\n  name: " + name + "\n  severity: info\nhttp:\n  - method: GET\n    path:\n      - \"{{BaseURL}}/\"\n    matchers:\n      - type: status\n        status:\n          - 200\n"
}

// An instance POC with a built-in id takes its place in the list: the node
// runs the instance one, and the built-in row is not shown beside it.
func TestAnInstancePocCoversABuiltInOfTheSameID(t *testing.T) {
	s := library(t)
	if _, err := s.Put("pocs/mine.yaml", pocAny("builtin-poc", "Mine"), ""); err != nil {
		t.Fatalf("Put with a built-in id = %v", err)
	}
	rows, matched, err := s.List(ListOptions{Kind: KindPOC})
	if err != nil || matched != 1 {
		t.Fatalf("POCs = %+v matched %d err %v; want one row", rows, matched, err)
	}
	if !rows[0].Covers || rows[0].Origin != OriginInstance || rows[0].ID != "builtin-poc" {
		t.Fatalf("the covering POC = %+v", rows[0])
	}
}

// A POC's finger field is what the scan pipeline selects it by, so the list
// carries it.
func TestAPocNamesTheFingerprintsItIsBoundTo(t *testing.T) {
	s := library(t)
	if _, err := s.Put("pocs/mine.yaml", "id: mine\ninfo:\n  name: Mine\nfinger:\n  - tomcat\n  - nginx\n", ""); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.List(ListOptions{Kind: KindPOC, Origin: OriginInstance})
	if err != nil || len(rows) != 1 || strings.Join(rows[0].Fingers, ",") != "tomcat,nginx" {
		t.Fatalf("instance POC = %+v err %v", rows, err)
	}
}

func TestPutChecksTheRevisionAndMovesTheTimeForward(t *testing.T) {
	s := library(t)
	rows, err := s.Put("pocs/brick-poc.yaml", pocYAML, "")
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(s.Dir, "pocs", "brick-poc.yaml")
	first, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("pocs/brick-poc.yaml", pocSameSize, ""); !errors.Is(err, ErrRevision) {
		t.Fatalf("overwrite without a revision = %v, want ErrRevision", err)
	}
	if _, err := s.Put("pocs/brick-poc.yaml", pocSameSize, "stale"); !errors.Is(err, ErrRevision) {
		t.Fatalf("overwrite with a stale revision = %v, want ErrRevision", err)
	}
	if _, err := s.Put("pocs/new.yaml", pocYAML, "some-revision"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new file with a revision = %v, want ErrNotFound", err)
	}
	rows, err = s.Put("pocs/brick-poc.yaml", pocSameSize, rows[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if first.Size() == second.Size() && !second.ModTime().After(first.ModTime()) {
		t.Fatal("a same-size rewrite did not move the modification time forward")
	}
	// The list reads the new content without waiting for anything else.
	if listed, _, _ := s.List(ListOptions{Kind: KindPOC, Origin: OriginInstance}); listed[0].Name != "Other POC" || listed[0].Revision != rows[0].Revision {
		t.Fatalf("listed = %+v, want the rewritten POC", listed[0])
	}
}

func TestContentReadsBothLayers(t *testing.T) {
	s := library(t)
	if _, err := s.Put("pocs/brick-poc.yaml", pocYAML, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Content(ContentRef{Kind: KindPOC, Origin: OriginInstance, Path: "pocs/brick-poc.yaml"}); err != nil || got != pocYAML {
		t.Fatalf("instance content = %q, %v", got, err)
	}
	// A built-in fingerprint comes back as the YAML list the node loads.
	got, err := s.Content(ContentRef{Kind: KindFinger, Origin: OriginBuiltin, Name: "nginx"})
	if err != nil || !strings.Contains(got, "name: nginx") || !strings.Contains(got, "tag:") {
		t.Fatalf("built-in fingerprint content = %q, %v", got, err)
	}
	if strings.Contains(got, "Attributes") {
		t.Fatalf("built-in fingerprint content still carries the empty attributes field: %q", got)
	}
	if got, err := s.Content(ContentRef{Kind: KindPOC, Origin: OriginBuiltin, Name: "builtin-poc"}); err != nil || !strings.Contains(got, "id: builtin-poc") || !strings.Contains(got, "severity: high") {
		t.Fatalf("built-in POC content = %q, %v", got, err)
	}
	if _, err := s.Content(ContentRef{Kind: KindPOC, Origin: OriginBuiltin, Name: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing built-in = %v, want ErrNotFound", err)
	}
	if _, err := s.Content(ContentRef{Kind: KindPOC, Origin: OriginInstance, Path: "../pocs/x.yaml"}); err == nil {
		t.Fatal("a path outside the library was read")
	}
}

func TestDeleteChecksTheRevision(t *testing.T) {
	s := library(t)
	rows, err := s.Put("pocs/brick-poc.yaml", pocYAML, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("pocs/brick-poc.yaml", ""); !errors.Is(err, ErrRevision) {
		t.Fatalf("delete without a revision = %v, want ErrRevision", err)
	}
	if err := s.Delete("pocs/brick-poc.yaml", rows[0].Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "pocs", "brick-poc.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file is still there: %v", err)
	}
	if err := s.Delete("pocs/brick-poc.yaml", rows[0].Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice = %v, want ErrNotFound", err)
	}
}

func TestImportSortsFilesByWhatTheyAre(t *testing.T) {
	s := library(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "http", "web.yaml"), fingerYAML)
	write(t, filepath.Join(src, "cve.yaml"), pocYAML)
	write(t, filepath.Join(src, "junk.yaml"), "just: a mapping\n")
	write(t, filepath.Join(src, "builtin.yaml"), "id: builtin-poc\ninfo:\n  name: Again\n")
	res, err := s.Import(src)
	if err != nil {
		t.Fatal(err)
	}
	// A POC with a built-in id is imported too: it covers the built-in one.
	if res.Imported != 3 || res.Skipped != 1 || len(res.Errors) != 1 {
		t.Fatalf("import = %+v", res)
	}
	for _, path := range []string{"fingers/http/web.yaml", "pocs/brick-poc.yaml", "pocs/builtin-poc.yaml"} {
		if _, err := os.Stat(filepath.Join(s.Dir, filepath.FromSlash(path))); err != nil {
			t.Errorf("%s was not imported: %v", path, err)
		}
	}
}

func TestStatusReportsWhatTheNodeWrote(t *testing.T) {
	s := library(t)
	if _, reported, err := s.Status(); err != nil || reported {
		t.Fatalf("status before the node reports = reported %v err %v", reported, err)
	}
	write(t, filepath.Join(s.Dir, NodeDir, nodeState), nodeStateJSON)
	state, reported, err := s.Status()
	if err != nil || !reported || state["available"] != true {
		t.Fatalf("status = reported %v %v err %v", reported, state, err)
	}
}

func TestAStoreWithoutADirectoryIsNotConfigured(t *testing.T) {
	s := New("")
	if _, err := s.Put("pocs/a.yaml", pocYAML, ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Put = %v, want ErrNotConfigured", err)
	}
}

func TestDerivePathNamesAPastedRule(t *testing.T) {
	if got, err := DerivePath(pocYAML, ""); err != nil || got != "pocs/brick-poc.yaml" {
		t.Fatalf("POC path = %q, %v", got, err)
	}
	finger := "- name: Seeyon Login\n  rule:\n    - regexps:\n        body:\n          - seeyon\n"
	if got, err := DerivePath(finger, "oa"); err != nil || got != "fingers/oa/seeyon-login.yaml" {
		t.Fatalf("fingerprint path = %q, %v", got, err)
	}
	if _, err := DerivePath("just: a mapping\n", ""); err == nil {
		t.Fatal("a mapping that is neither kind got a path")
	}
}

// The old library is copied once, into an empty library only, and the old
// files are left where they were.
func TestAdoptLegacyPOCsCopiesOnceAndLeavesTheOldFiles(t *testing.T) {
	data := t.TempDir()
	old, dir := filepath.Join(data, "pocs"), filepath.Join(data, "rules")
	write(t, filepath.Join(old, "a.yaml"), pocYAML)
	write(t, filepath.Join(old, "notes.txt"), "not a rule")
	write(t, filepath.Join(old, ".hidden.yaml"), pocYAML)

	copied, err := AdoptLegacyPOCs(old, dir)
	if err != nil || copied != 1 {
		t.Fatalf("first start copied %d, %v; want 1", copied, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "pocs", "a.yaml"))
	if err != nil || string(got) != pocYAML {
		t.Fatalf("copied file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(old, "a.yaml")); err != nil {
		t.Fatalf("the old file was moved away: %v", err)
	}

	// The library has a POC of its own now, so a later start copies nothing.
	write(t, filepath.Join(old, "b.yaml"), pocSameSize)
	if copied, err := AdoptLegacyPOCs(old, dir); err != nil || copied != 0 {
		t.Fatalf("second start copied %d, %v; want 0", copied, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pocs", "b.yaml")); !os.IsNotExist(err) {
		t.Fatalf("the second start copied a file: %v", err)
	}

	if copied, err := AdoptLegacyPOCs(filepath.Join(data, "missing"), filepath.Join(data, "elsewhere")); err != nil || copied != 0 {
		t.Fatalf("no old library = %d, %v; want 0", copied, err)
	}
}
