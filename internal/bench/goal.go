package bench

import (
	"fmt"
	"strings"
)

// GoalStatement is the goal text for one challenge. One goal per challenge, not
// one goal for the task: with three containers alive their progress differs, and
// a shared goal would make a completion receipt unable to say which challenge it
// was about. The statement has to be judgeable by the platform, so it says
// "every flag of THIS challenge is judged correct" rather than anything about
// effort or coverage.
func GoalStatement(ch Challenge) string {
	desc := ""
	if ch.Description != nil {
		desc = strings.TrimSpace(*ch.Description)
	}
	if desc == "" {
		return fmt.Sprintf("本题（%s）的全部 flag 已被平台判定为 correct。", ch.UniqueCode)
	}
	return fmt.Sprintf("本题（%s）：%s；全部 flag 已被平台判定为 correct。", ch.UniqueCode, desc)
}

// FinalScope is the scope text written once on shutdown, from the last snapshot
// the run actually saw.
//
// The dangerous state is leaving the previous address list in place: those
// addresses may already belong to another challenge (the platform hands the same
// IP to the next start), and a commander reading them would attack somebody
// else's box. So the final text always comes from the last snapshot and never
// from the document that is already stored:
//
//   - something is still available → name exactly those addresses;
//   - nothing is available → say so, with the snapshot's own file name, and name
//     no address at all. An empty list is stated as a sentence, not as an empty
//     document: a cleared document reads as "no restriction".
func FinalScope(snap Snapshot, snapshotFile string) (string, error) {
	snapshotFile = strings.TrimSpace(snapshotFile)
	if snapshotFile == "" {
		// Without the file name the text cannot be checked against the platform
		// bytes later, and this is the one write that must stay auditable.
		return "", fmt.Errorf("bench: final scope needs the snapshot file it was derived from")
	}
	if addrs := snap.AllowedAddrs(); len(addrs) > 0 {
		text, err := ScopeText(addrs)
		if err != nil {
			return "", err
		}
		return text, nil
	}
	return fmt.Sprintf("本次测评没有进行中的题（快照 %s）。\n当前没有任何可触及的地址；不要使用此前出现过的地址。", snapshotFile), nil
}
