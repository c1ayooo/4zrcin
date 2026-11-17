package api

import (
	"sync"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

func TestProjectSettingsConcurrentRoutingAndBriefing(t *testing.T) {
	s, _ := newBindingServer(t)
	s.SetProjectSettings("scope-a", "description-a")
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 1000; i++ {
			name := "a"
			if i%2 != 0 {
				name = "b"
			}
			s.SetProjectSettings("scope-"+name, "description-"+name)
		}
	})
	wg.Go(func() {
		for i := 0; i < 1000; i++ {
			brief := s.Briefing()
			if brief != "scope-a\n\n项目描述：\ndescription-a" && brief != "scope-b\n\n项目描述：\ndescription-b" {
				t.Errorf("mixed briefing snapshot: %q", brief)
				return
			}
			if _, _, err := s.planRun(model.Intent{ID: "fixture"}, ""); err != nil {
				t.Error(err)
				return
			}
			_ = s.scopeText()
		}
	})
	wg.Wait()
}
