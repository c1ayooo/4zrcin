package aop

// The ToolEnv payload contract lives in aop/payloads. DisableNativeIOASendEnv
// is the shell's own env contract (the console mirror sets it), not a payload.
import (
	"strings"

	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	ToolEnvExtensionURL = payloads.ToolEnvExtensionURL
	// DisableNativeIOASendEnv disables the harness's native IOA send command
	// for sessions the console supervises (the console routes IOA itself).
	DisableNativeIOASendEnv = "CYBER_IOA_SEND_DISABLED"
)

// ToolEnvExtension encodes the environment map. Empty — or a map whose every
// key is blank — returns nil, so a blank-key entry never creates an extension.
func ToolEnvExtension(env map[string]string) *anypb.Any {
	for key := range env {
		if strings.TrimSpace(key) != "" {
			return payloads.ToolEnvExtension(env)
		}
	}
	return nil
}
