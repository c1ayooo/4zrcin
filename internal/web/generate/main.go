//go:build ignore

// Command generate turns the built console (web/dist) into the embedded
// bundle the web package serves. It is the platform-side twin of the harness
// generator (tools/resources/templates_gen.go): flate-compressed data files
// plus a generated manifest, deterministic given the same input tree.
//
//	go generate ./internal/web          # after pnpm --dir web build
//	go run ./internal/web/generate -check   # verify the committed bundle matches web/dist
//
// The gate runs both halves: it rebuilds web/dist, regenerates the bundle and
// fails when the regeneration changed anything — a committed bundle that no
// longer matches web/dist cannot ship (docs/04 §4.1). Everything lands in
// internal/web/bundle (package bundle): one directory, one freshness snapshot.
//
//go:generate go run generate/main.go
package main

import (
	"bytes"
	"compress/flate"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/buildinfo"
)

// entry is one manifest row: the URL path (relative to the console root),
// the original bytes' identity, and the compressed blob carrying it.
type entry struct {
	Path string
	Hash string
	Size int64
	Comp int
	Data string
}

func main() {
	src := flag.String("src", "", "built console directory (default: $ZRCIN_WEB_DIR, then the sibling 4zweb checkout's dist, then the legacy web/dist)")
	out := flag.String("out", "bundle", "output directory (internal/web/bundle)")
	check := flag.Bool("check", false, "verify existing outputs instead of writing")
	flag.Parse()

	dir := resolveSource(*src)
	snap, err := buildinfo.SnapshotTree(dir)
	if err != nil {
		fail("读取 %s：%v", dir, err)
	}
	if !snap.Exists || len(snap.Files) == 0 {
		fail("%s 不存在或为空：先在 4zweb 里 pnpm build", dir)
	}
	if _, ok := snap.Files["index.html"]; !ok {
		fail("%s 缺少 index.html：这不是一次完整的 console 构建", dir)
	}

	paths := snap.Paths()
	entries := make([]entry, 0, len(paths))
	datas := map[string][]byte{}
	for i, p := range paths {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			fail("读取 %s：%v", p, err)
		}
		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, flate.BestCompression)
		if err != nil {
			fail("flate：%v", err)
		}
		if _, err := w.Write(raw); err != nil {
			fail("压缩 %s：%v", p, err)
		}
		if err := w.Close(); err != nil {
			fail("收尾 %s：%v", p, err)
		}
		name := fmt.Sprintf("bundle_data/%04d.bin", i)
		entries = append(entries, entry{
			Path: p,
			Hash: snap.Files[p].SHA256,
			Size: snap.Files[p].Size,
			Comp: buf.Len(),
			Data: name,
		})
		datas[name] = buf.Bytes()
	}
	sourceHash := snap.AggregateSHA256()
	consoleCommit := consoleCommitOf(dir)

	gen := renderGen(sourceHash, consoleCommit, entries)
	if *check {
		existing, err := os.ReadFile(filepath.Join(*out, "bundle_gen.go"))
		if err != nil {
			fail("bundle_gen.go 不存在：%v", err)
		}
		if !bytes.Equal(existing, gen) {
			fail("bundle 落后于 %s：运行 go generate ./internal/web 并提交产物", dir)
		}
		for name, want := range datas {
			have, err := os.ReadFile(filepath.Join(*out, filepath.FromSlash(name)))
			if err != nil || !bytes.Equal(have, want) {
				fail("bundle 落后于 %s（%s）：运行 go generate ./internal/web 并提交产物", dir, name)
			}
		}
		return
	}

	if err := os.MkdirAll(filepath.Join(*out, "bundle_data"), 0o755); err != nil {
		fail("建目录：%v", err)
	}
	// Remove stale data files first: a smaller dist would otherwise leave old
	// bins embedded forever.
	old, _ := filepath.Glob(filepath.Join(*out, "bundle_data", "*.bin"))
	for _, p := range old {
		_ = os.Remove(p)
	}
	for name, data := range datas {
		if err := os.WriteFile(filepath.Join(*out, filepath.FromSlash(name)), data, 0o644); err != nil {
			fail("写 %s：%v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(*out, "bundle_gen.go"), gen, 0o644); err != nil {
		fail("写 bundle_gen.go：%v", err)
	}
	total := 0
	for _, e := range entries {
		total += e.Comp
	}
	if consoleCommit != "" {
		fmt.Printf("（console commit %s）", consoleCommit)
	}
	fmt.Printf("bundle: %d 个文件，压缩后 %d 字节（source %s）", len(entries), total, shortHash(sourceHash))
}

// resolveSource finds the built console: an explicit -src wins, then
// $ZRCIN_WEB_DIR, then the sibling 4zweb checkout's dist (the split repo,
// docs/19 P1-3), then the legacy in-tree web/dist.
func resolveSource(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := strings.TrimSpace(os.Getenv("ZRCIN_WEB_DIR")); env != "" {
		return env
	}
	candidates := []string{"../../../4zweb/dist", "../../web/dist"}
	if cwd, err := os.Getwd(); err == nil {
		for _, candidate := range candidates {
			if abs, err := filepath.Abs(filepath.Join(cwd, candidate)); err == nil {
				if info, err := os.Stat(filepath.Join(abs, "index.html")); err == nil && !info.IsDir() {
					return abs
				}
			}
		}
	}
	return candidates[0]
}

// consoleCommitOf returns the 4zweb checkout's short commit (the dist's
// parent repo), or "" when it cannot be determined — the build output stays
// usable, only the provenance line is missing.
func consoleCommitOf(dist string) string {
	repo := filepath.Dir(dist)
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func renderGen(sourceHash, consoleCommit string, entries []entry) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by internal/web/generate; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// The console bundle: one flate-compressed file per web/dist entry, plus\n")
	b.WriteString("// the manifest the server looks paths up in. sourceHash is the aggregate\n")
	b.WriteString("// digest of the input tree (buildinfo.TreeSnapshot), so a review can tell\n")
	b.WriteString("// which dist build produced these bytes.\n\n")
	b.WriteString("//go:build !noconsole\n\n")
	b.WriteString("package bundle\n\n")
	b.WriteString("import \"embed\"\n\n")
	b.WriteString("//go:embed bundle_data\nvar Data embed.FS\n\n")
	fmt.Fprintf(&b, "// SourceHash is the aggregate sha256 of the web/dist tree that\n// produced this bundle (%d files). The gate compares a fresh dist digest\n// against it.\nconst SourceHash = %q\n\n", len(entries), sourceHash)
	b.WriteString("// Entry is one console file: the URL path, the original byte count and\n// hash, and the compressed blob carrying it.\ntype Entry struct {\n\tPath string\n\tHash string\n\tSize int64\n\tComp int\n\tData string\n}\n\n")
	fmt.Fprintf(&b, "\n// ConsoleCommit is the 4zweb checkout commit the dist was built from\n// (recorded by the generator, empty when unavailable).\nconst ConsoleCommit = %q\n", consoleCommit)
	b.WriteString("// Entries is the console manifest, sorted by URL path.\nvar Entries = []Entry{\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "\t{%q, %q, %d, %d, %q},\n", e.Path, e.Hash, e.Size, e.Comp, e.Data)
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "generate: "+format+"\n", args...)
	os.Exit(1)
}
