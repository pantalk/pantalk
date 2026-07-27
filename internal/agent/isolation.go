package agent

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Isolation modes.
const (
	IsolationNone      = "none"
	IsolationContainer = "container"
)

// DefaultWorkspace is the in-container directory an isolated agent works in,
// and the mount point of its private workspace volume.
const DefaultWorkspace = "/workspace"

// DefaultContainerRuntime is the CLI used to start containers. Podman is
// argument-compatible for everything Wrap emits, so `runtime: podman` is the
// only change needed there.
const DefaultContainerRuntime = "docker"

// defaultImages maps a harness binary to the image that ships it. Only
// harnesses with an official published image belong here: an agent naming any
// other harness must supply `image:` explicitly rather than have Pantalk guess
// a name that may not exist.
var defaultImages = map[string]string{
	"zot": "ghcr.io/openzot/openzot:latest",
}

// Isolation describes where an agent's harness runs. It is policy rather than
// mechanism: the fields say what the agent gets, not how it is enforced, so a
// future backend can satisfy the same declaration without a config change.
//
// YAML accepts either the shorthand scalar:
//
//	isolation: container
//
// or the full form:
//
//	isolation:
//	  mode: container
//	  image: ghcr.io/acme/zot-with-terraform:v3
type Isolation struct {
	Mode      string `yaml:"mode"`
	Image     string `yaml:"image"`
	Workspace string `yaml:"workspace"` // in-container working directory (default /workspace)
	Runtime   string `yaml:"runtime"`   // container CLI (default docker)
}

// UnmarshalYAML accepts the scalar shorthand as well as the mapping form.
func (i *Isolation) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		mode := strings.TrimSpace(value.Value)
		if mode == "" {
			return nil
		}
		*i = Isolation{Mode: mode}
		return nil
	}

	// A named type avoids recursing into this method.
	type isolationFields Isolation
	var fields isolationFields
	if err := value.Decode(&fields); err != nil {
		return err
	}
	*i = Isolation(fields)
	return nil
}

// Enabled reports whether the agent's harness runs in a container.
func (i Isolation) Enabled() bool {
	return strings.TrimSpace(i.Mode) == IsolationContainer
}

// Validate checks the declaration on its own. Resolving the image needs the
// harness name, so that is reported by Wrap.
func (i Isolation) Validate() error {
	switch strings.TrimSpace(i.Mode) {
	case "", IsolationNone, IsolationContainer:
		return nil
	default:
		return fmt.Errorf("unknown isolation mode %q (want %q or %q)", i.Mode, IsolationContainer, IsolationNone)
	}
}

// workspace returns the in-container working directory.
func (i Isolation) workspace() string {
	if trimmed := strings.TrimSpace(i.Workspace); trimmed != "" {
		return trimmed
	}
	return DefaultWorkspace
}

// Workdir is the working directory an isolated agent should be given. It is a
// path inside the container, so it deliberately ignores any host workdir.
func (i Isolation) Workdir() string {
	if !i.Enabled() {
		return ""
	}
	return i.workspace()
}

// image resolves the container image for a harness.
func (i Isolation) image(harness string) (string, error) {
	if trimmed := strings.TrimSpace(i.Image); trimmed != "" {
		return trimmed, nil
	}
	if known, ok := defaultImages[harness]; ok {
		return known, nil
	}
	return "", fmt.Errorf("no default image for harness %q; set isolation.image", harness)
}

// VolumeName is the private workspace volume for an agent. Each agent gets its
// own, which is what keeps one agent's work — and later its rendered harness
// config — out of reach of the others.
func VolumeName(agentName string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, strings.TrimSpace(agentName))
	return "pantalk-" + sanitized + "-workspace"
}

// Wrap turns a harness invocation into a container invocation. The harness
// command is preserved as the entrypoint rather than appended as arguments,
// because images commonly declare the harness as their own ENTRYPOINT and
// appending would run it twice (`zot zot acp`).
//
// env names the variables to forward. Values are not passed on the command
// line — `-e NAME` forwards from the runtime process, so a credential never
// appears in the argv of a process any user on the host can list.
func (i Isolation) Wrap(agentName string, command Command, env map[string]string) (Command, error) {
	if !i.Enabled() {
		return command, nil
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("agent %q: isolation requires a command naming the harness", agentName)
	}

	harness := command[0]
	image, err := i.image(harness)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", agentName, err)
	}

	runtime := strings.TrimSpace(i.Runtime)
	if runtime == "" {
		runtime = DefaultContainerRuntime
	}
	workspace := i.workspace()

	wrapped := Command{
		runtime, "run", "--rm", "--interactive",
		"--entrypoint", harness,
		"--volume", VolumeName(agentName) + ":" + workspace,
		"--workdir", workspace,
	}

	// Sorted so the argv is stable across restarts and testable.
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		wrapped = append(wrapped, "--env", name)
	}

	wrapped = append(wrapped, image)
	wrapped = append(wrapped, command[1:]...)
	return wrapped, nil
}
