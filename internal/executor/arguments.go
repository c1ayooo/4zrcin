package executor

import (
	"encoding/base64"
	"encoding/json"
)

// PlainToolArguments rewrites one raw tool_call event so that its `arguments`
// field carries the text the harness sent instead of base64.
//
// The protocol's Arguments is bytes; protojson encodes bytes as base64, so the
// command was unreadable in the journal text — and, worse, invisible to the
// journal's redaction: it replaces known secrets in the encoded line, and a key
// sitting inside base64 never matches. Decoding here, at the writer, is the
// single place that fixes both: the journal then holds one shape, the original
// text, and no reader has to decode anything.
//
// The second result names a protocol anomaly ("" when there is none). It is not
// swallowed: an arguments object this cannot decode is still stored as text, so
// the journal keeps one shape, and the reason travels on the event's Warning so
// the broken call is visible where it happened.
func PlainToolArguments(raw json.RawMessage) (json.RawMessage, string) {
	if len(raw) == 0 {
		return raw, ""
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return raw, ""
	}
	callRaw, ok := outer["tool_call"]
	if !ok {
		return raw, ""
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(callRaw, &call); err != nil {
		return raw, "tool_call 不是对象，工具参数未改写"
	}
	argsRaw, ok := call["arguments"]
	if !ok {
		// A tool with no arguments has no arguments field; nothing to rewrite.
		return raw, ""
	}
	plain, problem, rewrite := plainArguments(argsRaw)
	if !rewrite {
		return raw, problem
	}
	text, err := json.Marshal(plain)
	if err != nil {
		return raw, "工具参数写不成文本：" + err.Error()
	}
	call["arguments"] = text
	rebuiltCall, err := json.Marshal(call)
	if err != nil {
		return raw, problem
	}
	outer["tool_call"] = rebuiltCall
	rebuilt, err := json.Marshal(outer)
	if err != nil {
		return raw, problem
	}
	return rebuilt, problem
}

// plainArguments turns the protocol's encoded arguments into the text they
// carry. rewrite=false leaves the event alone: its arguments are already text.
func plainArguments(argsRaw json.RawMessage) (plain string, problem string, rewrite bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(argsRaw, &obj); err != nil {
		// Not an object. A string is already the original text; anything else
		// (a number, an array) is not a shape the protocol produces, so it is
		// kept as text and named rather than left as a second shape.
		var text string
		if json.Unmarshal(argsRaw, &text) == nil {
			return "", "", false
		}
		return string(argsRaw), "工具参数既不是字符串也不是对象：原样存为文本", true
	}
	for key := range obj {
		if key != "data" && key != "media_type" {
			// Not the protocol's EncodedValue (e.g. a bare {"command": …}): keep
			// its text so the journal still carries what the caller sent.
			return string(argsRaw), "工具参数不是协议的编码值：原样存为文本", true
		}
	}
	var encoded struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(argsRaw, &encoded); err != nil {
		return string(argsRaw), "工具参数无法解码：" + err.Error(), true
	}
	if encoded.Data == "" {
		// An encoded value with no bytes is a call with no arguments.
		return "", "", true
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded.Data)
	if err != nil {
		return string(argsRaw), "工具参数无法按协议解码（base64 无效）：原样存为文本，命令索引看不到这条", true
	}
	return string(decoded), "", true
}
