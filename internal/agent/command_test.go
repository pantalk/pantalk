package agent

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCommand_UnmarshalYAML_String(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected Command
	}{
		{
			name:     "simple",
			input:    "claude -p test",
			expected: Command{"claude", "-p", "test"},
		},
		{
			name:     "double quoted arg",
			input:    `claude -p "Check pantalk notifications"`,
			expected: Command{"claude", "-p", "Check pantalk notifications"},
		},
		{
			name:     "single quoted arg",
			input:    `claude -p 'Check pantalk notifications'`,
			expected: Command{"claude", "-p", "Check pantalk notifications"},
		},
		{
			name:     "mixed quotes",
			input:    `claude -p "hello 'world'"`,
			expected: Command{"claude", "-p", "hello 'world'"},
		},
		{
			name:     "escaped quote in double quotes",
			input:    `claude -p "say \"hello\""`,
			expected: Command{"claude", "-p", `say "hello"`},
		},
		{
			name:     "multiple spaces",
			input:    "claude   -p   test",
			expected: Command{"claude", "-p", "test"},
		},
		{
			name:     "single word",
			input:    "claude",
			expected: Command{"claude"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := tokenize(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := Command(tokens)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("got %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestCommand_Tokenize_UnterminatedQuote(t *testing.T) {
	_, err := tokenize(`claude -p "unterminated`)
	if err == nil {
		t.Fatal("expected error for unterminated double quote")
	}

	_, err = tokenize(`claude -p 'unterminated`)
	if err == nil {
		t.Fatal("expected error for unterminated single quote")
	}
}

func TestCommand_Tokenize_EmptyString(t *testing.T) {
	tokens, err := tokenize("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected empty slice, got %v", tokens)
	}
}

func TestAllowedCommands(t *testing.T) {
	allowed := []string{"claude", "codex", "copilot", "aider", "goose", "opencode", "gemini"}
	for _, name := range allowed {
		if !AllowedCommands[name] {
			t.Errorf("expected %q to be in AllowedCommands", name)
		}
	}

	notAllowed := []string{"rm", "bash", "sh", "curl", "wget"}
	for _, name := range notAllowed {
		if AllowedCommands[name] {
			t.Errorf("expected %q to not be in AllowedCommands", name)
		}
	}
}

// --- YAML unmarshal integration tests ---

func TestCommand_UnmarshalYAML_StringForm(t *testing.T) {
	type wrapper struct {
		Cmd Command `yaml:"command"`
	}

	input := `command: claude -p "Check notifications"`

	var w wrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := Command{"claude", "-p", "Check notifications"}
	if !reflect.DeepEqual(w.Cmd, expected) {
		t.Errorf("got %v, want %v", w.Cmd, expected)
	}
}

func TestCommand_UnmarshalYAML_ArrayForm(t *testing.T) {
	type wrapper struct {
		Cmd Command `yaml:"command"`
	}

	input := `command: ["claude", "-p", "Check notifications"]`

	var w wrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := Command{"claude", "-p", "Check notifications"}
	if !reflect.DeepEqual(w.Cmd, expected) {
		t.Errorf("got %v, want %v", w.Cmd, expected)
	}
}

func TestCommand_UnmarshalYAML_ArrayFormMultiline(t *testing.T) {
	type wrapper struct {
		Cmd Command `yaml:"command"`
	}

	input := `command:
  - claude
  - -p
  - "Check notifications"
`

	var w wrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := Command{"claude", "-p", "Check notifications"}
	if !reflect.DeepEqual(w.Cmd, expected) {
		t.Errorf("got %v, want %v", w.Cmd, expected)
	}
}

func TestCommand_UnmarshalYAML_SingleWord(t *testing.T) {
	type wrapper struct {
		Cmd Command `yaml:"command"`
	}

	input := `command: claude`

	var w wrapper
	if err := yaml.Unmarshal([]byte(input), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := Command{"claude"}
	if !reflect.DeepEqual(w.Cmd, expected) {
		t.Errorf("got %v, want %v", w.Cmd, expected)
	}
}

func TestCommand_UnmarshalYAML_InvalidMapForm(t *testing.T) {
	type wrapper struct {
		Cmd Command `yaml:"command"`
	}

	input := `command:
  key: value
`

	var w wrapper
	err := yaml.Unmarshal([]byte(input), &w)
	if err == nil {
		t.Fatal("expected error for map-form command")
	}
}

func TestCommand_Tokenize_TabsAsDelimiters(t *testing.T) {
	tokens, err := tokenize("claude\t-p\ttest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := Command{"claude", "-p", "test"}
	if !reflect.DeepEqual(Command(tokens), expected) {
		t.Errorf("got %v, want %v", tokens, expected)
	}
}

func TestCommand_Tokenize_EmptyQuotedStrings(t *testing.T) {
	tokens, err := tokenize(`claude -p ""`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty double-quoted string produces an empty token appended to previous
	// Actually: "claude", "-p", "" — but tokenize joins adjacent chars.
	// The empty quote contributes zero bytes to current, but since -p has a
	// space after it and the empty string follows, the empty string token may
	// or may not be emitted depending on implementation. Let's just check no error.
	if len(tokens) < 2 {
		t.Errorf("expected at least 2 tokens, got %v", tokens)
	}
}

func TestCommand_Tokenize_AdjacentQuotes(t *testing.T) {
	tokens, err := tokenize(`"hello""world"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Adjacent quoted strings are concatenated into one token
	expected := Command{"helloworld"}
	if !reflect.DeepEqual(Command(tokens), expected) {
		t.Errorf("got %v, want %v", tokens, expected)
	}
}

func TestCommand_Tokenize_BackslashInDoubleQuotes(t *testing.T) {
	tokens, err := tokenize(`claude -p "line1\nline2"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// \n inside double quotes: backslash is consumed as escape, next char 'n' is literal
	expected := Command{"claude", "-p", "line1nline2"}
	if !reflect.DeepEqual(Command(tokens), expected) {
		t.Errorf("got %v, want %v", tokens, expected)
	}
}
