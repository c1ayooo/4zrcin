package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// The PostgreSQL contract step must fail loudly without a database (setup
// guidance, not a silent skip), honor the explicit -skip-pg opt-out, and put
// the server version in the step name so the manifest records what ran
// (docs/17 §3, N5).
func TestPGContractStep(t *testing.T) {
	cfg := config{}
	t.Run("no dsn is a failure with guidance", func(t *testing.T) {
		t.Setenv("ZRCIN_TEST_PG_DSN", "")
		step := pgContractStep(cfg, ".")
		err := step.run()
		if err == nil {
			t.Fatal("missing ZRCIN_TEST_PG_DSN must fail the gate")
		}
		var skip skipStep
		if errors.As(err, &skip) {
			t.Fatalf("missing database must not be a skip: %v", err)
		}
		for _, want := range []string{"ZRCIN_TEST_PG_DSN", "scripts/test-pg.sh", "-skip-pg"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("guidance missing %q: %v", want, err)
			}
		}
	})
	t.Run("explicit skip is recorded", func(t *testing.T) {
		t.Setenv("ZRCIN_TEST_PG_DSN", "")
		skip := config{skipPG: true}
		step := pgContractStep(skip, ".")
		err := step.run()
		var s skipStep
		if !errors.As(err, &s) {
			t.Fatalf("-skip-pg must produce a recorded skip, got %T %v", err, err)
		}
	})
	t.Run("step name carries the server version", func(t *testing.T) {
		dsn := strings.TrimSpace(envOr(t, "ZRCIN_TEST_PG_DSN"))
		if dsn == "" {
			t.Skip("no ZRCIN_TEST_PG_DSN: start scripts/test-pg.sh to exercise the probe")
		}
		step := pgContractStep(config{}, ".")
		if !strings.Contains(step.name, "server ") {
			t.Fatalf("step name missing server version: %q", step.name)
		}
	})
}

func envOr(t *testing.T, key string) string {
	t.Helper()
	return testEnv(key)
}

func testEnv(key string) string { return os.Getenv(key) }
