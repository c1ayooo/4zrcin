package api

import "strings"

// withBoardAnchor completes a synthetic commander prompt with the insertion
// anchor. Production round prompts always carry "## board\n" because
// decider.CommanderRoundPrompt renders it; tests that exercise StartCommander's
// plumbing pass minimal strings. The layout insert requires the anchor — a
// missing anchor fails the round instead of appending silently — so those
// fixtures go through here.
func withBoardAnchor(prompt string) string {
	if strings.Contains(prompt, promptBoardAnchor) {
		return prompt
	}
	return prompt + "\n" + promptBoardAnchor + "{}\n"
}
