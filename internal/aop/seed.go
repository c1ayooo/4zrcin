package aop

import (
	"encoding/base64"
	"encoding/json"

	"google.golang.org/protobuf/types/known/anypb"
)

// CaseSeedExtensionURL is the per-turn fork extension that carries the case
// files a node without a shared filesystem must read (docs/15 §8 Z1). The
// harness writes them under the turn workspace before the model runs; an old
// harness ignores the unknown extension, so sending it is additive.
const CaseSeedExtensionURL = "type.googleapis.com/4zrcin.CaseSeed"

// SeedFile is one case file travelling with a turn. Paths are relative to the
// turn's WorkDir.
type SeedFile struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
	Data []byte `json:"-"`
}

type seedWireFile struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
	Data string `json:"data"`
}

type seedPayload struct {
	Files []seedWireFile `json:"files"`
}

// CaseSeedExtension encodes the files, or nil when there is nothing to send so
// a local turn keeps exactly the previous wire bytes.
func CaseSeedExtension(files []SeedFile) *anypb.Any {
	if len(files) == 0 {
		return nil
	}
	payload := seedPayload{Files: make([]seedWireFile, 0, len(files))}
	for _, file := range files {
		payload.Files = append(payload.Files, seedWireFile{
			Path: file.Path,
			Mode: file.Mode,
			Data: base64.StdEncoding.EncodeToString(file.Data),
		})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return &anypb.Any{TypeUrl: CaseSeedExtensionURL, Value: raw}
}
