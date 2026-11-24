package settings

import (
	"os"
	"path/filepath"
	"testing"
)

// The instance review default has three states on disk: never saved (follow the
// --no-verify boot flag), saved open, saved closed. The pointer is what keeps the
// first one distinguishable — a bool would turn "never chose" into a default
// written on the operator's behalf.
func TestReviewsDefaultFallsBackToBootOnlyWhenUnset(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "settings.json")
	s, err := LoadAt(legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s.RuntimeSettings().ReviewsDisabledOf(); ok || v {
		t.Fatalf("fresh store reports stored %v / %v; want never set", v, ok)
	}
	for _, boot := range []bool{true, false} {
		if got := s.RuntimeSettings().ReviewsDefaultDisabled(boot); got != boot {
			t.Fatalf("no stored value with boot=%v → default %v", boot, got)
		}
	}
}

func TestReviewsDefaultSavedValueWinsOverBoot(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "settings.json")
	s, err := LoadAt(legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	closed := true
	if err := s.SetRuntime(Runtime{AuxiliaryConcurrency: 1, ReviewsDisabled: &closed}); err != nil {
		t.Fatal(err)
	}
	if got := s.RuntimeSettings().ReviewsDefaultDisabled(false); !got {
		t.Fatal("a saved closed default lost to the open boot flag")
	}
	reloaded, err := LoadAt(legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := reloaded.RuntimeSettings().ReviewsDisabledOf()
	if !ok || !v {
		t.Fatalf("reloaded store = %v / %v; want a stored true", v, ok)
	}
	if got := reloaded.RuntimeSettings().ReviewsDefaultDisabled(false); !got {
		t.Fatal("a saved closed default did not survive a reload")
	}

	open := false
	if err := reloaded.SetRuntime(Runtime{AuxiliaryConcurrency: 1, ReviewsDisabled: &open}); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.RuntimeSettings().ReviewsDefaultDisabled(true); got {
		t.Fatal("a saved open default lost to the closed boot flag")
	}
}

// A hand-written file (the console writes it, an operator may too) is read the
// same way, and a file without the field stays "never set".
func TestReviewsDefaultReadsHandWrittenFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(legacy, []byte(`{"model":{},"runtime":{"reviews_disabled":true}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadAt(legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.RuntimeSettings().ReviewsDefaultDisabled(false); !got {
		t.Fatal("hand-written reviews_disabled:true was ignored")
	}

	plain := filepath.Join(dir, "plain.json")
	if err := os.WriteFile(plain, []byte(`{"model":{}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := LoadAt(plain, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.RuntimeSettings().ReviewsDisabledOf(); ok {
		t.Fatal("a file without reviews_disabled was read as an explicit value")
	}
	if got := s2.RuntimeSettings().ReviewsDefaultDisabled(true); !got {
		t.Fatal("a file without reviews_disabled lost the boot fallback")
	}
}

// The auxiliary gate keeps its own contract: the review default rides in the same
// struct, so a save that only means to set the review default must not be able to
// slip an invalid concurrency past Validate.
func TestReviewsDefaultSharesValidateWithConcurrency(t *testing.T) {
	closed := true
	rt := Runtime{AuxiliaryConcurrency: 0, ReviewsDisabled: &closed}
	if err := rt.Validate(); err == nil {
		t.Fatal("Validate accepted auxiliary_concurrency 0 alongside a review default")
	}
}
