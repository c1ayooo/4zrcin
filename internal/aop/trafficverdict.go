package aop

// The verdict payload contract lives in aop/payloads; the type is aliased so
// the fence's verdicts and this decoder cannot drift.
import (
	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const TrafficVerdictExtensionURL = payloads.TrafficVerdictExtensionURL

type TrafficVerdict = payloads.TrafficVerdict

func TrafficVerdictExtension(v TrafficVerdict) *anypb.Any { return payloads.TrafficVerdictExtension(v) }

func ParseTrafficVerdict(ext *anypb.Any) (TrafficVerdict, bool) {
	return payloads.ParseTrafficVerdict(ext)
}
