package api

import (
	"sync"
	"testing"

	"github.com/kksty/tianwu/internal/model"
)

func TestProjectSettingsConcurrentRoutingAndBriefing(t *testing.T) {
	s, _ := projectTierServer(t, nil)
	s.SetProjectSettings("scope-a", "description-a", &ProfileDefaults{Worker: "cheap", Strong: "strong"})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 1000; i++ {
			name := "a"
			if i%2 != 0 {
				name = "b"
			}
			s.SetProjectSettings("scope-"+name, "description-"+name, &ProfileDefaults{Worker: "cheap", Strong: "strong"})
		}
	})
	wg.Go(func() {
		for i := 0; i < 1000; i++ {
			brief := s.Briefing()
			if brief != "scope-a\n\n项目描述：\ndescription-a" && brief != "scope-b\n\n项目描述：\ndescription-b" {
				t.Errorf("mixed briefing snapshot: %q", brief)
				return
			}
			if _, _, err := s.planRun(model.Intent{ID: "fixture"}, "", "", ""); err != nil {
				t.Error(err)
				return
			}
			if _, _, _, err := s.resolveCommanderProfile(); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.spawnProfile("", "worker"); err != nil {
				t.Error(err)
				return
			}
			_ = s.scopeText()
		}
	})
	wg.Wait()
}

func TestProjectSettingsOwnProfileSnapshot(t *testing.T) {
	s := &Server{}
	profiles := &ProfileDefaults{Worker: "cheap", Strong: "strong"}
	s.SetProjectSettings("scope", "description", profiles)
	profiles.Worker = "changed"
	if got := s.projectProfileDefaults(); got.Worker != "cheap" {
		t.Fatalf("caller changed the live profile snapshot: %+v", got)
	}
	s.SetProjectSettings("", "", nil)
	if got := s.projectProfileDefaults(); got != (ProfileDefaults{}) || s.Briefing() != "" {
		t.Fatal("clearing settings retained the old snapshot")
	}
}
