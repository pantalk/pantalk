package agent

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Command is an argv slice that can be unmarshaled from either a YAML string
// or a YAML sequence. When given as a string, it is tokenized using
// shell-like quoting rules (respecting single and double quotes) but never
// passed through an actual shell.
type Command []string

// UnmarshalYAML implements yaml.Unmarshaler so Command accepts both:
//
//	command: claude -p "Check notifications"
//	command: ["claude", "-p", "Check notifications"]
func (c *Command) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		tokens, err := tokenize(value.Value)
		if err != nil {
			return fmt.Errorf("parse command string: %w", err)
		}
		*c = tokens
		return nil

	case yaml.SequenceNode:
		var items []string
		if err := value.Decode(&items); err != nil {
			return err
		}
		*c = items
		return nil

	default:
		return fmt.Errorf("command must be a string or list, got %v", value.Kind)
	}
}

// tokenize splits a command string into tokens, respecting single and double
// quotes. This is intentionally simple — no variable expansion, no escapes
// beyond backslash inside double quotes, no globbing.
func tokenize(s string) ([]string, error) {
	var tokens []string
	var current []byte
	i := 0

	for i < len(s) {
		ch := s[i]

		switch {
		case ch == ' ' || ch == '\t':
			if len(current) > 0 {
				tokens = append(tokens, string(current))
				current = nil
			}
			i++

		case ch == '\'':
			// Single-quoted string: take everything until closing quote.
			i++
			for i < len(s) && s[i] != '\'' {
				current = append(current, s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated single quote")
			}
			i++ // skip closing quote

		case ch == '"':
			// Double-quoted string: allow backslash escapes.
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					current = append(current, s[i])
				} else {
					current = append(current, s[i])
				}
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated double quote")
			}
			i++ // skip closing quote

		default:
			current = append(current, ch)
			i++
		}
	}

	if len(current) > 0 {
		tokens = append(tokens, string(current))
	}

	return tokens, nil
}
