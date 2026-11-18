package aopexec

// Session adoption after a ResetSession.
//
// A resume can discover that the host no longer knows the recorded native
// session (a restart or a replaced host DB answers NOT_FOUND). The control
// plane's recovery is to ask the host to replace that session and to record the
// replacement in the case pointer, so the retried turn continues on the new
// session instead of opening a third, untracked one.
//
// This file extends session.go deliberately without touching the Run loop:
// executor.go owns dispatch and is frozen for the parallel integration work.

import "errors"

// AdoptSessionForResume rewrites the case's AOP session pointer to a replacement
// session. The previous node/url/agent stay; only the session id and the stale
// turn/cursor (which belonged to the lost session) are replaced. A case without
// an existing pointer is an error: adopting a session that no run ever opened
// would fabricate a trajectory.
func AdoptSessionForResume(workDir, sessionID string) error {
	if sessionID == "" {
		return errors.New("替换 session 的 id 为空")
	}
	m, ok := readMarker(workDir)
	if !ok {
		return errors.New("案例没有 AOP 会话标记，无法接管替换会话")
	}
	m.Session = sessionID
	m.Turn = ""
	m.Cursor = ""
	return writeMarker(workDir, m)
}
