package agent

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestIsolationUnmarshalScalarShorthand(t *testing.T) {
	var isolation Isolation
	if err := yaml.Unmarshal([]byte("container\n"), &isolation); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !isolation.Enabled() || isolation.Mode != IsolationContainer {
		t.Fatalf("isolation = %#v", isolation)
	}
}

func TestIsolationUnmarshalBlock(t *testing.T) {
	var isolation Isolation
	input := "mode: container\nimage: ghcr.io/acme/zot:v3\nworkspace: /srv/work\nruntime: podman\n"
	if err := yaml.Unmarshal([]byte(input), &isolation); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !isolation.Enabled() || isolation.Image != "ghcr.io/acme/zot:v3" ||
		isolation.Workspace != "/srv/work" || isolation.Runtime != "podman" {
		t.Fatalf("isolation = %#v", isolation)
	}
}

func TestIsolationValidate(t *testing.T) {
	for _, mode := range []string{"", IsolationNone, IsolationContainer} {
		if err := (Isolation{Mode: mode}).Validate(); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
	if err := (Isolation{Mode: "vm"}).Validate(); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
}

// A disabled isolation must leave the invocation exactly as configured, so
// existing agents keep running the harness directly.
func TestWrapWithoutIsolationIsIdentity(t *testing.T) {
	command := Command{"zot", "acp"}
	wrapped, err := Isolation{}.Wrap("reviewer", command, nil)
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	if strings.Join(wrapped, " ") != "zot acp" {
		t.Fatalf("wrapped = %#v", wrapped)
	}
}

func TestWrapBuildsContainerInvocation(t *testing.T) {
	isolation := Isolation{Mode: IsolationContainer}
	wrapped, err := isolation.Wrap("reviewer", Command{"zot", "acp"}, map[string]string{
		"CHATBOTKIT_API_SECRET": "$CBK_TOKEN",
	})
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}

	got := strings.Join(wrapped, " ")
	want := "docker run --rm --interactive --entrypoint zot " +
		"--volume pantalk-reviewer-workspace:/workspace --workdir /workspace " +
		"--env CHATBOTKIT_API_SECRET ghcr.io/openzot/openzot:latest acp"
	if got != want {
		t.Fatalf("wrapped =\n  %s\nwant\n  %s", got, want)
	}
}

// Images commonly declare the harness as their ENTRYPOINT, so the harness must
// be passed via --entrypoint. Appending it as an argument would run it twice.
func TestWrapPassesHarnessAsEntrypointNotArgument(t *testing.T) {
	wrapped, err := Isolation{Mode: IsolationContainer}.Wrap("r", Command{"zot", "acp"}, nil)
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	for i, arg := range wrapped {
		if arg == "ghcr.io/openzot/openzot:latest" {
			if rest := strings.Join(wrapped[i+1:], " "); rest != "acp" {
				t.Fatalf("args after image = %q, want just the harness args", rest)
			}
			return
		}
	}
	t.Fatalf("image not present in %#v", wrapped)
}

func TestWrapHonoursOverrides(t *testing.T) {
	isolation := Isolation{
		Mode:      IsolationContainer,
		Image:     "ghcr.io/acme/custom:v1",
		Workspace: "/srv/work",
		Runtime:   "podman",
	}
	wrapped, err := isolation.Wrap("docs", Command{"zot", "acp"}, nil)
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}

	got := strings.Join(wrapped, " ")
	for _, want := range []string{
		"podman run", "--entrypoint zot", "--workdir /srv/work",
		"pantalk-docs-workspace:/srv/work", "ghcr.io/acme/custom:v1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped = %q, missing %q", got, want)
		}
	}
}

// Guessing an image name for a harness that has no official one would produce
// a pull failure at start instead of a config error.
func TestWrapRequiresImageForUnknownHarness(t *testing.T) {
	_, err := Isolation{Mode: IsolationContainer}.Wrap("helper", Command{"goose", "acp"}, nil)
	if err == nil || !strings.Contains(err.Error(), "isolation.image") {
		t.Fatalf("error = %v, want a message naming isolation.image", err)
	}
}

// Each agent gets its own volume; that separation is what later makes writing
// harness config into the agent's own home safe.
func TestVolumeNameIsPerAgentAndSanitized(t *testing.T) {
	if got := VolumeName("Code Reviewer"); got != "pantalk-code-reviewer-workspace" {
		t.Fatalf("VolumeName() = %q", got)
	}
	if VolumeName("a") == VolumeName("b") {
		t.Fatal("agents must not share a workspace volume")
	}
}

func TestWorkdirOnlyAppliesWhenIsolated(t *testing.T) {
	if got := (Isolation{}).Workdir(); got != "" {
		t.Fatalf("Workdir() = %q, want empty for a non-isolated agent", got)
	}
	if got := (Isolation{Mode: IsolationContainer}).Workdir(); got != DefaultWorkspace {
		t.Fatalf("Workdir() = %q", got)
	}
}
