package aop

import (
	"encoding/json"

	"google.golang.org/protobuf/types/known/anypb"
)

// ArtifactExtensionURL is the 4zrcin fork's evidence sidecar (fork patch 23c,
// docs/04): a remote scan node ships its case-directory output as
// 4zrcin.Artifact extensions on the events the run already produces, so the
// bytes travel the Envelope stream instead of a shared filesystem. The payload
// is JSON, like every other fork extension:
//
//	{"path": "results.jsonl", "kind": "process", "media_type": "application/octet-stream",
//	 "size": 123, "sha256": "…", "data": "<base64>"}
//
// The hub lands each artifact in the central case directory (.native, via
// internal/artifact.Save) and keeps the path as the artifact identity. Old
// zrcin ignores the unknown sidecar; old harnesses never send it.
const ArtifactExtensionURL = "type.googleapis.com/4zrcin.Artifact"

// MaxArtifactBytes matches internal/artifact.Save's cap: the wire cannot carry
// something the case store would refuse.
const MaxArtifactBytes = 16 << 20

// Artifact is one decoded evidence sidecar.
type Artifact struct {
	Path      string `json:"path"`
	Kind      string `json:"kind,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	Data      []byte `json:"data,omitempty"`
}

// Valid reports whether the sidecar carries landable bytes with a stable
// identity. A path-less or empty artifact is dropped, never guessed into the
// case directory.
func (a Artifact) Valid() bool {
	return a.Path != "" && len(a.Data) > 0 && len(a.Data) <= MaxArtifactBytes
}

// ParseArtifact decodes one sidecar Any. ok is false for anything that is not
// a landable artifact of this namespace; the caller keeps such events
// untouched instead of guessing.
func ParseArtifact(ext *anypb.Any) (Artifact, bool) {
	if ext == nil || ext.GetTypeUrl() != ArtifactExtensionURL {
		return Artifact{}, false
	}
	var a Artifact
	if err := json.Unmarshal(ext.GetValue(), &a); err != nil {
		return Artifact{}, false
	}
	if !a.Valid() {
		return Artifact{}, false
	}
	return a, true
}
