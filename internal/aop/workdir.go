package aop

// The WorkDir payload contract lives in aop/payloads (single source for both
// sides). These aliases keep the shell's call sites unchanged.
import (
	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const WorkDirExtensionURL = payloads.WorkDirExtensionURL

func WorkDirExtension(dir string) *anypb.Any { return payloads.WorkDirExtension(dir) }
