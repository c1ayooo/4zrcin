package settings

import (
	"path/filepath"
	"strings"
	"testing"
)

func validFence() Fence {
	return Fence{
		EnabledCommands: true,
		Commands: []GlobalCommandRule{
			{Pattern: `shutdown\s+-h\b`, Desc: "关机", Action: "deny"},
			{Pattern: `^curl\b`, Action: "review"},
		},
		EnabledDomains: true,
		Domains: []GlobalDomainRule{
			{Host: "*.gov.example"},
			{Host: "10.0.0.0/8"},
		},
	}
}

// Normalization fills what the operator left off and refuses what the node
// could not judge honestly: an uncompilable pattern would silently stop
// fencing the moment it matched.
func TestNormalizeFence(t *testing.T) {
	got, err := NormalizeFence(validFence())
	if err != nil {
		t.Fatal(err)
	}
	if !got.EnabledCommands || !got.EnabledDomains || len(got.Commands) != 2 || len(got.Domains) != 2 {
		t.Fatalf("normalized = %+v", got)
	}
	// Defaults: action deny, generated non-positional ids.
	if got.Commands[0].Action != "deny" || got.Commands[1].Action != "review" {
		t.Fatalf("actions = %q/%q", got.Commands[0].Action, got.Commands[1].Action)
	}
	for _, row := range got.Commands {
		if row.ID == "" || !strings.HasPrefix(row.ID, "gf-") {
			t.Fatalf("command id not generated: %+v", row)
		}
	}
	for _, row := range got.Domains {
		if row.ID == "" || !strings.HasPrefix(row.ID, "gf-") {
			t.Fatalf("domain id not generated: %+v", row)
		}
	}
	// The wildcard spelling canonicalizes onto the bare domain, exactly what
	// the project fence stores — one domain, one row, either list.
	if got.Domains[0].Host != "gov.example" {
		t.Fatalf("host = %q, want the bare domain", got.Domains[0].Host)
	}
}

func TestNormalizeFenceRejections(t *testing.T) {
	cases := []Fence{
		{EnabledCommands: true, Commands: []GlobalCommandRule{{Pattern: "(", Action: "deny"}}},
		{EnabledCommands: true, Commands: []GlobalCommandRule{{Pattern: "ok", Action: "nuke"}}},
		{EnabledCommands: true, Commands: []GlobalCommandRule{{Pattern: "", Action: "deny"}}},
		{EnabledDomains: true, Domains: []GlobalDomainRule{{Host: "a..example"}}},
		{EnabledDomains: true, Domains: []GlobalDomainRule{{ID: "same", Host: "a.example"}, {ID: "same", Host: "b.example"}}},
		{EnabledCommands: true, Commands: func() []GlobalCommandRule {
			rows := make([]GlobalCommandRule, MaxFenceCommands+1)
			for i := range rows {
				rows[i] = GlobalCommandRule{Pattern: "ok", Action: "deny"}
			}
			return rows
		}()},
	}
	for i, fence := range cases {
		if _, err := NormalizeFence(fence); err == nil {
			t.Fatalf("case %d: impossible fence accepted", i)
		}
	}
}

// Operator ids are stable identity: kept verbatim (charset-checked), unique
// across both lists.
func TestNormalizeFenceKeepsOperatorIDs(t *testing.T) {
	got, err := NormalizeFence(Fence{
		EnabledCommands: true,
		Commands:        []GlobalCommandRule{{ID: "shutdown-line", Pattern: "shutdown", Action: "deny"}},
		EnabledDomains:  true,
		Domains:         []GlobalDomainRule{{ID: "gov-cn", Host: "gov.example"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Commands[0].ID != "shutdown-line" || got.Domains[0].ID != "gov-cn" {
		t.Fatalf("operator ids not preserved: %+v", got)
	}
	if _, err := NormalizeFence(Fence{
		EnabledCommands: true,
		Commands:        []GlobalCommandRule{{ID: "bad id!", Pattern: "ok", Action: "deny"}},
	}); err == nil {
		t.Fatal("an id with illegal characters was accepted")
	}
}

// The fence is durable instance state: a restart reads back the normalized
// rows, and an unset fence reads as zero (nothing fenced, nothing on the
// wire).
func TestFencePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.FenceOf(); !got.IsEmpty() {
		t.Fatalf("a fresh store fenced something: %+v", got)
	}
	if err := store.SetFence(validFence()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.FenceOf()
	if !got.EnabledDomains || len(got.Domains) != 2 || got.Domains[0].Host != "gov.example" {
		t.Fatalf("reopened fence = %+v", got)
	}
	// A model-only save must not wipe the fence (the same contract the
	// runtime knobs have).
	if err := reopened.SetFence(Fence{}); err != nil {
		t.Fatal(err)
	}
	if !reopened.FenceOf().IsEmpty() {
		t.Fatal("clearing the fence did not clear it")
	}
}

// FenceOf hands out an independent copy: a caller decoding a request into the
// returned rows must not reach the stored fence through a shared backing
// array. This is the regression that let a rejected PUT mutate rows it never
// saved.
func TestFenceOfCannotMutateStoredRows(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFence(Fence{
		EnabledCommands: true,
		Commands:        []GlobalCommandRule{{ID: "gf-1", Pattern: "shutdown", Desc: "关机", Action: FenceActionDeny}},
		Domains:         []GlobalDomainRule{{ID: "gf-2", Host: "evil.example"}},
	}); err != nil {
		t.Fatal(err)
	}
	read := st.FenceOf()
	read.Commands[0].Pattern = "("
	read.Commands[0].Desc = "changed"
	read.Domains[0].Host = "changed.example"
	read.Commands = append(read.Commands, GlobalCommandRule{ID: "gf-3", Pattern: "x"})
	stored := st.FenceOf()
	if len(stored.Commands) != 1 || stored.Commands[0].Pattern != "shutdown" || stored.Commands[0].Desc != "关机" {
		t.Fatalf("stored commands changed through the read: %+v", stored.Commands)
	}
	if stored.Domains[0].Host != "evil.example" {
		t.Fatalf("stored domain changed through the read: %+v", stored.Domains)
	}
}

// The preset corpus compiles (it runs through the same normalization a save
// does), and ApplyPresetCommands is idempotent by id and pattern: a second
// load adds nothing, and an operator row covering a preset pattern is kept
// without a duplicate.
func TestApplyPresetCommands(t *testing.T) {
	if _, added, err := ApplyPresetCommands(Fence{}); err != nil || added != len(PresetCommands()) {
		t.Fatalf("first load: added=%d err=%v", added, err)
	}
	edited := Fence{Commands: []GlobalCommandRule{
		{ID: "op-wget", Pattern: `\bwget\b`, Desc: "操作员自己的行", Action: FenceActionDeny},
	}}
	merged, added, err := ApplyPresetCommands(edited)
	if err != nil {
		t.Fatal(err)
	}
	if added != len(PresetCommands())-1 {
		t.Fatalf("added = %d, want %d", added, len(PresetCommands())-1)
	}
	var wget GlobalCommandRule
	for _, r := range merged.Commands {
		if r.ID == "op-wget" {
			wget = r
		}
	}
	if wget.Action != FenceActionDeny || wget.Desc != "操作员自己的行" {
		t.Fatalf("operator row overwritten: %+v", wget)
	}
	if _, added, err := ApplyPresetCommands(merged); err != nil || added != 0 {
		t.Fatalf("second load: added=%d err=%v", added, err)
	}
}
