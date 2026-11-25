package aopexec

import (
	"strings"
	"testing"

	aop "github.com/c1ayooo/4zrcin/internal/aop"
	pb "github.com/c1ayooo/4zrcin/internal/aop/pb"
	"github.com/c1ayooo/4zrcin/internal/aop/tool"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestArtifactEventRetainsOriginalOutsideJournalJSON(t *testing.T) {
	value := &tool.Artifact{ResultId: "result-1", Tool: "neutron", Kind: "finding", Data: []byte("private native bytes")}
	ext, err := anypb.New(value)
	if err != nil {
		t.Fatal(err)
	}
	ev := event(&pb.Event{Id: "event-1", Payload: &pb.Event_Extension{Extension: ext}})
	if ev.Native == nil || ev.Native.ResultId != "result-1" || string(ev.Native.Data) != "private native bytes" || ev.NativeEventID != "event-1" || ev.Kind != "artifact" {
		t.Fatalf("native artifact lost: %+v", ev)
	}
	if strings.Contains(string(ev.Raw), "private native bytes") || strings.Contains(string(ev.Raw), "aop.tool.Artifact") {
		t.Fatal("raw artifact duplicated into journal")
	}
}

// The remote evidence sidecar (4zrcin.Artifact, fork patch 23c) rides any
// observed event — process completions carry the batch — and lands in the
// executor event's transport-only Artifacts list, never in the journal JSON.
func TestRemoteArtifactSidecarsCollected(t *testing.T) {
	sidecar, err := aopArtifactSidecar(`{"path":"results.jsonl","kind":"process","media_type":"application/octet-stream","size":7,"sha256":"abc","data":"eyJ2IjoxfQ=="}`)
	if err != nil {
		t.Fatal(err)
	}
	ev := event(&pb.Event{Id: "event-2", Extensions: []*anypb.Any{sidecar}})
	if len(ev.Artifacts) != 1 {
		t.Fatalf("sidecar not collected: %+v", ev)
	}
	art := ev.Artifacts[0]
	if art.Path != "results.jsonl" || art.Kind != "process" || string(art.Data) != `{"v":1}` || art.Size != 7 {
		t.Fatalf("artifact decoded wrong: %+v", art)
	}
	if strings.Contains(string(ev.Raw), "eyJ2IjoxfQ==") {
		t.Fatal("sidecar bytes duplicated into journal raw")
	}
	// A foreign sidecar stays untouched.
	foreign := &anypb.Any{TypeUrl: "type.googleapis.com/4zrcin.TrafficVerdict", Value: []byte(`{"action":"record"}`)}
	ev = event(&pb.Event{Id: "event-3", Extensions: []*anypb.Any{foreign}})
	if len(ev.Artifacts) != 0 {
		t.Fatalf("foreign sidecar misread as artifact: %+v", ev.Artifacts)
	}
}

func aopArtifactSidecar(body string) (*anypb.Any, error) {
	return &anypb.Any{TypeUrl: aop.ArtifactExtensionURL, Value: []byte(body)}, nil
}
