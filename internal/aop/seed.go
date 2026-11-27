package aop

// The case-seed payload contract lives in aop/payloads. The shell enforces
// the contract's limits here, before encoding: the executor already ranks
// candidates most-important-first and truncates under pressure (skills first,
// knowledge-base copies last), so dropping overflow keeps that order instead
// of failing a dispatch that was always going to be truncated on the node.
import (
	"github.com/chainreactors/cyber/aop/payloads"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	CaseSeedExtensionURL = payloads.CaseSeedExtensionURL
	// MaxSeedFileBytes / MaxSeedFiles / MaxSeedBytes are the contract limits
	// the node enforces on apply; the encoder below truncates to them first.
	MaxSeedFileBytes = payloads.MaxSeedFileBytes
	MaxSeedFiles     = payloads.MaxSeedFiles
	MaxSeedBytes     = payloads.MaxSeedBytes
)

type SeedFile = payloads.CaseSeedFile

// CaseSeedExtension encodes the files, or nil when there is nothing to send so
// a local turn keeps exactly the previous wire bytes. Overflow beyond the
// contract's limits is dropped (trailing files, then oversized ones) — the
// executor's ranking decides what survives, the node never refuses the turn
// for size.
func CaseSeedExtension(files []SeedFile) *anypb.Any {
	if len(files) == 0 {
		return nil
	}
	kept := make([]SeedFile, 0, min(len(files), MaxSeedFiles))
	total := 0
	for _, file := range files {
		if len(kept) >= MaxSeedFiles {
			break
		}
		if len(file.Data) > MaxSeedFileBytes {
			continue
		}
		if total+len(file.Data) > MaxSeedBytes {
			break
		}
		kept = append(kept, file)
		total += len(file.Data)
	}
	if len(kept) == 0 {
		return nil
	}
	ext, err := payloads.CaseSeedExtension(kept)
	if err != nil {
		return nil // unreachable: the caps above guarantee the encode fits
	}
	return ext
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
