package api

import (
	"encoding/json"
	"path"
	"strings"
	"unicode"
)

func toolCommand(raw json.RawMessage) string {
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	if call := body["tool_call"]; len(call) > 0 {
		return toolCommand(call)
	}
	if command := body["command"]; len(command) > 0 {
		var s string
		_ = json.Unmarshal(command, &s)
		return s
	}
	args := body["arguments"]
	if len(args) == 0 {
		return ""
	}
	// The producer decodes the protocol's bytes at write time, so the journal
	// carries the arguments as the original text (a JSON string).
	if args[0] == '"' {
		var text string
		if json.Unmarshal(args, &text) != nil || text == "" {
			return ""
		}
		return toolCommand(json.RawMessage(text))
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(args, &obj) != nil {
		return ""
	}
	if command := obj["command"]; len(command) > 0 {
		var s string
		_ = json.Unmarshal(command, &s)
		return s
	}
	return ""
}

// A conservative lexical observation, never a shell evaluator. Quoted text
// stays one argument; only command positions count, so `echo curl` is not curl.
func shellCommands(command string) []string {
	var words []string
	var word strings.Builder
	quote := rune(0)
	escaped := false
	comment := false
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range command {
		if comment {
			if r != '\n' {
				continue
			}
			comment = false
		}
		if r == '#' && quote == 0 && word.Len() == 0 {
			comment = true
			continue
		}
		if escaped {
			word.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ';' || r == '|' || r == '&' || r == '\n' {
			flush()
			words = append(words, ";")
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		word.WriteRune(r)
	}
	flush()
	var result []string
	var segment []string
	parse := func() {
		i := 0
		for i < len(segment) {
			s := segment[i]
			if strings.Contains(s, "=") || s == "env" || s == "command" || s == "sudo" || s == "nohup" {
				i++
				continue
			}
			break
		}
		if i >= len(segment) {
			return
		}
		name := strings.ToLower(strings.TrimSuffix(path.Base(strings.ReplaceAll(segment[i], "\\", "/")), ".exe"))
		if (name == "bash" || name == "sh") && i+2 < len(segment) && strings.Contains(segment[i+1], "c") {
			result = append(result, shellCommands(segment[i+2])...)
			return
		}
		if !strings.HasPrefix(name, "$") && !strings.HasPrefix(name, "#") && !strings.ContainsAny(name, "<>()`") {
			result = append(result, name)
		}
	}
	for _, word := range words {
		if word == ";" {
			parse()
			segment = nil
		} else {
			segment = append(segment, word)
		}
	}
	parse()
	return result
}
