package aop

// The egress-proxy payload contract lives in aop/payloads.
import (
	"encoding/json"

	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const EgressProxyExtensionURL = payloads.EgressProxyExtensionURL

func EgressProxyExtension(proxy json.RawMessage) *anypb.Any {
	return payloads.EgressProxyExtension(proxy)
}
