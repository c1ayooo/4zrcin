package board

import (
	"time"

	"github.com/c1ayooo/4zrcin/internal/model"
	"github.com/c1ayooo/4zrcin/internal/store"
)

// TokenUsage observes consumption. No field is a limit or an admission policy.
type TokenUsage struct {
	TotalTokens       int `json:"total_tokens"`
	HourTokens        int `json:"hour_tokens"`
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	CacheWriteTokens  int `json:"cache_write_tokens"`
	Requests          int `json:"requests"`
	MissingUsage      int `json:"missing_usage"`
	UnknownRuns       int `json:"unknown_usage_runs"`
}

func Tokens(st *store.Store, now time.Time) TokenUsage {
	out := TokenUsage{}
	if st == nil {
		return out
	}
	return TokensFromRuns(st.Runs(), now)
}

// TokensFromRuns aggregates over runs a caller already froze (one Snapshot for
// a whole fleet build): re-reading the store live would aggregate a different
// instant than the rest of the snapshot and split counts from the run list.
func TokensFromRuns(runs []model.Run, now time.Time) TokenUsage {
	out := TokenUsage{}
	for _, r := range runs {
		// Tokens is retained on old runs that predate detailed Usage.
		total := r.Tokens
		if r.Usage.Known {
			total = r.Usage.TotalTokens
			out.InputTokens += r.Usage.InputTokens
			out.OutputTokens += r.Usage.OutputTokens
			out.CachedInputTokens += r.Usage.CachedInputTokens
			out.CacheWriteTokens += r.Usage.CacheWriteTokens
			out.Requests += r.Usage.Requests
			out.MissingUsage += r.Usage.Missing
		} else {
			out.UnknownRuns++
		}
		out.TotalTokens += total
		if r.StartedAt.After(now.Add(-time.Hour)) && !r.StartedAt.After(now) {
			out.HourTokens += total
		}
	}
	return out
}
