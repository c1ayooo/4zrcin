package aop

// The RunGoal payload contract lives in aop/payloads.
import (
	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const RunGoalExtensionURL = payloads.RunGoalExtensionURL

func RunGoalExtension(goal, evalRounds string) *anypb.Any {
	return payloads.RunGoalExtension(goal, evalRounds)
}
