package aop

// The evidence payload contract lives in aop/payloads; the wire cap is the
// engine's 8 MiB (the node never ships more), replacing the shell's old 16.
import (
	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	ArtifactExtensionURL = payloads.ArtifactExtensionURL
	// MaxArtifactBytes is the engine's artifactsync wire limit — unified by
	// the contract package after the two sides disagreed (16 here, 8 there).
	MaxArtifactBytes = payloads.MaxArtifactBytes
)

type Artifact = payloads.Artifact

func ParseArtifact(ext *anypb.Any) (Artifact, bool) { return payloads.ParseArtifact(ext) }
