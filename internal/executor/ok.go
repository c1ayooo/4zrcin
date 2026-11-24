package executor

// BoolPtr records an outcome bit on an event (tool_result / turn_ended). The
// field is a pointer so "this kind carries no outcome" and "it failed" stay
// distinct in the journal: a plain bool with omitempty dropped the false, and a
// failed tool_result then looked exactly like a text event to anything reading
// the log as text.
func BoolPtr(v bool) *bool { return &v }

// IsOK reports a recorded success. Absent is not success.
func (e Event) IsOK() bool { return e.OK != nil && *e.OK }

// NotOK reports a recorded failure. Only an explicit false counts: a missing bit
// means the event carries no outcome, and that is not evidence that a tool
// failed.
func (e Event) NotOK() bool { return e.OK != nil && !*e.OK }
