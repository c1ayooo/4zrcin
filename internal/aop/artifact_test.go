package aop

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"
)

func artifactJSON(path string, size int) []byte {
	body := `{"path":"` + path + `","kind":"process","media_type":"text/plain","size":` + itoa(size) + `,"sha256":"abc","data":"` + strings.Repeat("QQ==", 1) + `"}`
	return []byte(body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestParseArtifactAcceptsValidSidecar(t *testing.T) {
	ext := &anypb.Any{TypeUrl: ArtifactExtensionURL, Value: artifactJSON("results.jsonl", 4)}
	art, ok := ParseArtifact(ext)
	if !ok {
		t.Fatal("valid sidecar rejected")
	}
	if art.Path != "results.jsonl" || art.Kind != "process" || art.MediaType != "text/plain" || string(art.Data) != "A" || art.Size != 4 {
		t.Fatalf("decoded payload wrong: %+v", art)
	}
}

func TestParseArtifactRejectsForeignAndBroken(t *testing.T) {
	if _, ok := ParseArtifact(&anypb.Any{TypeUrl: "type.googleapis.com/4zrcin.TrafficVerdict", Value: []byte(`{}`)}); ok {
		t.Fatal("foreign URL accepted")
	}
	if _, ok := ParseArtifact(&anypb.Any{TypeUrl: ArtifactExtensionURL, Value: []byte(`not json`)}); ok {
		t.Fatal("broken JSON accepted")
	}
	if _, ok := ParseArtifact(&anypb.Any{TypeUrl: ArtifactExtensionURL, Value: []byte(`{"path":"","data":"QQ=="}`)}); ok {
		t.Fatal("path-less artifact accepted")
	}
	if _, ok := ParseArtifact(&anypb.Any{TypeUrl: ArtifactExtensionURL, Value: []byte(`{"path":"x"}`)}); ok {
		t.Fatal("empty-bytes artifact accepted")
	}
	if _, ok := ParseArtifact(nil); ok {
		t.Fatal("nil accepted")
	}
	// Over-cap bytes are refused on the wire, not truncated into the case store.
	huge := `{"path":"x","data":"` + strings.Repeat("QQ==", MaxArtifactBytes) + `"}`
	if _, ok := ParseArtifact(&anypb.Any{TypeUrl: ArtifactExtensionURL, Value: []byte(huge)}); ok {
		t.Fatal("over-cap artifact accepted")
	}
}

func TestArtifactExtensionURLIsTheForkNamespace(t *testing.T) {
	// One literal on both sides of the wire: harness core/artifactsync defines
	// the same URL, and a drift here must fail loudly, not silently strand the
	// remote evidence in an unread sidecar.
	if ArtifactExtensionURL != "type.googleapis.com/4zrcin.Artifact" {
		t.Fatalf("URL drifted: %s", ArtifactExtensionURL)
	}
}
