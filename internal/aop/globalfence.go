package aop

// The global-fence payload contract lives in aop/payloads.
import (
	"encoding/json"

	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const GlobalFenceExtensionURL = payloads.GlobalFenceExtensionURL

func GlobalFenceExtension(fence json.RawMessage) *anypb.Any {
	return payloads.GlobalFenceExtension(fence)
}
