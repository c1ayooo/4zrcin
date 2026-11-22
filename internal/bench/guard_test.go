package bench

import (
	"os/exec"
	"strings"
	"testing"
)

// The promise given to the user is that a project which was not configured for
// the benchmark runs exactly the code it ran before. A comment cannot hold that
// promise; the dependency graph can. If anyone ever writes `if benchMode` inside
// the decider or adds an import of this package to the control plane, this test
// goes red before the change can be merged.
func TestControlPlaneCannotReachTheBenchPackage(t *testing.T) {
	out := goList(t, "list", "-deps", "./internal/decider", "./internal/api")
	if strings.Contains(out, "tianwu/internal/bench") {
		t.Fatalf("internal/decider or internal/api depends on internal/bench:\n%s", out)
	}
}

// Only the `tw` command may import the benchmark client. Keeping the importer
// set to exactly one package is what makes "bench is an isolated entry point"
// checkable instead of a habit.
func TestOnlyTheTwCommandImportsTheBenchPackage(t *testing.T) {
	out := goList(t, "list", "-f", `{{.ImportPath}}|{{join .Imports " "}}`, "./...")
	const pkg = "github.com/kksty/tianwu/internal/bench"
	allowed := map[string]bool{
		"github.com/kksty/tianwu/cmd/tw": true,
		pkg:                              true, // its own tests
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		imp, imports, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		for _, dep := range strings.Fields(imports) {
			if dep == pkg && !allowed[imp] {
				t.Fatalf("%s imports internal/bench; only cmd/tw may", imp)
			}
		}
	}
}

func goList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = "../.." // module root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
