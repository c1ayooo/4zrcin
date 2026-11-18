package buildinfo

// A one-screen human summary of a manifest. Both commands print it so the gate
// log itself carries the traceability facts, not only the JSON file.

import (
	"fmt"
	"strings"
)

// Describe renders the key fields for terminal output. Paths and values come
// straight from the manifest, so the printed summary and the JSON file cannot
// disagree.
func Describe(m *Manifest, manifestOut string) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "清单：%s\n", manifestOut)
	fmt.Fprintf(&b, "  source    commit=%s branch=%s dirty=%v patch_sha256=%s\n",
		dash(m.Source.Commit), dash(m.Source.Branch), m.Source.Dirty, dash(m.Source.PatchSHA256))
	for _, sub := range m.Source.Submodules {
		fmt.Fprintf(&b, "  submodule %s commit=%s initialized=%v\n", sub.Path, dash(sub.Commit), sub.Initialized)
	}
	fmt.Fprintf(&b, "  harness   dir=%s commit=%s dirty=%s\n",
		dash(m.Harness.SourceDir), dashPtr(m.Harness.Commit), valueOrDash(m.Harness.Dirty))
	if m.AOPProto != nil {
		if m.AOPProto.Status == AOPProtoStatusSkipped {
			fmt.Fprintf(&b, "  aop_proto status=skipped fingerprint=%s reason=%s\n",
				dash(m.AOPProto.SHA256), dashPtr(m.AOPProto.Reason))
		} else {
			fmt.Fprintf(&b, "  aop_proto status=%s fingerprint=%s files=%d harness_commit=%s mismatches=%d\n",
				m.AOPProto.Status, dash(m.AOPProto.SHA256), len(m.AOPProto.Files),
				dashPtr(m.AOPProto.HarnessCommit), len(m.AOPProto.Mismatches))
		}
	}
	for _, bin := range m.Harness.Binaries {
		fmt.Fprintf(&b, "  %-9s %s sha256=%s version=%s\n", bin.Name, bin.Path, dash(bin.SHA256), versionLine(bin))
	}
	for _, bin := range m.ControlPlane.Binaries {
		fmt.Fprintf(&b, "  %-9s %s sha256=%s version=%s\n", bin.Name, bin.Path, dash(bin.SHA256), versionLine(bin))
	}
	fmt.Fprintf(&b, "  versions  ioa=%d store_schema=%d bundle=%d bundle_project=%d graph=%d\n",
		m.Versions.IOAProtocol, m.Versions.StoreSchema, m.Versions.Bundle, m.Versions.BundleProject, m.Versions.Graph)
	fmt.Fprintf(&b, "  platform  %s/%s %s node=%s pnpm=%s\n",
		m.Platform.GOOS, m.Platform.GOARCH, m.Platform.GOVersion,
		dashPtr(m.Platform.NodeVersion), dashPtr(m.Platform.PNPMVersion))
	fmt.Fprintf(&b, "  model     profile=%s provider=%s configured=%s base_url=%s\n",
		dashPtr(m.Model.ActiveProfile), dashPtr(m.Model.Provider), dashPtr(m.Model.ModelID), dashPtr(m.Model.BaseURL))
	if m.Model.Probe != nil {
		probe := m.Model.Probe
		switch {
		case !probe.Probed:
			fmt.Fprintf(&b, "  probe     skipped reason=%s\n", dashPtr(probe.Reason))
		case probe.Error != nil:
			fmt.Fprintf(&b, "  probe     failed returned_model=%s error=%s\n", dashPtr(probe.ReturnedModel), *probe.Error)
		default:
			fmt.Fprintf(&b, "  probe     returned_model=%s upstream_model=%s\n",
				dashPtr(probe.ReturnedModel), dashPtr(probe.UpstreamModel))
		}
	}
	if m.Gate != nil && m.Gate.Embedded != nil {
		fmt.Fprintf(&b, "  embedded  before=%s after=%s changed=%d\n",
			dash(m.Gate.Embedded.BeforeSHA256), dash(m.Gate.Embedded.AfterSHA256), len(m.Gate.Embedded.Changed))
	}
	return b.String()
}

func versionLine(b Binary) string {
	if b.Missing {
		return "缺失"
	}
	if b.VersionError != nil {
		return "不可用（" + clipText(*b.VersionError, 80) + "）"
	}
	if b.Version == "" {
		return "(空)"
	}
	return clipText(b.Version, 80)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func dashPtr(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func valueOrDash(b *bool) string {
	if b == nil {
		return "-"
	}
	if *b {
		return "true"
	}
	return "false"
}
