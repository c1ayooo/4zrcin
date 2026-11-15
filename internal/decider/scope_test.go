package decider

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestScopeUpdatesWhileCommanderBuildsRounds(t *testing.T) {
	st := newStore(t)
	t.Cleanup(func() { _ = st.Close() })
	r := NewRunner(st, nil, "")
	r.ModelName = "fixture"
	r.SetScope("scope-before")
	r.Commander = func(_ context.Context, prompt string, _ bool) (RoundResult, error) {
		if !strings.Contains(prompt, "scope-before") && !strings.Contains(prompt, "scope-after") {
			t.Error("round lost the authorized scope")
		}
		return RoundResult{}, nil
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 2000; i++ {
			r.SetScope("scope-after")
		}
	})
	for i := 0; i < 5; i++ {
		if r.Run(context.Background(), "operator", true) == nil {
			t.Fatal("round did not run")
		}
	}
	wg.Wait()
}
