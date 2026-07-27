package procenv

import (
	"os/exec"
	"slices"
	"testing"
)

func TestApplySetsExactlyTheConfiguredEntries(t *testing.T) {
	cmd := &exec.Cmd{}
	Apply(cmd, map[string]string{
		"B_SECOND": "2",
		"A_FIRST":  "1",
	})

	want := []string{"A_FIRST=1", "B_SECOND=2"}
	if !slices.Equal(cmd.Env, want) {
		t.Fatalf("environment = %v, want %v", cmd.Env, want)
	}
}

func TestApplyDoesNotInheritDaemonEnvironment(t *testing.T) {
	t.Setenv("PANTALK_PROCENV_TEST", "inherited")

	cmd := &exec.Cmd{}
	Apply(cmd, map[string]string{"OVERRIDE": "value"})

	if slices.Contains(cmd.Env, "PANTALK_PROCENV_TEST=inherited") {
		t.Fatalf("daemon environment leaked into the child: %v", cmd.Env)
	}
}

func TestApplyReplacesAnyPreexistingEnvironment(t *testing.T) {
	cmd := &exec.Cmd{Env: []string{"PRESET=1"}}
	Apply(cmd, map[string]string{"ONLY": "value"})

	if !slices.Equal(cmd.Env, []string{"ONLY=value"}) {
		t.Fatalf("environment = %v, want [ONLY=value]", cmd.Env)
	}
}

// Without entries the environment must still be set, because a nil Env makes
// exec fall back to inheriting the daemon's.
func TestApplyEmptiesEnvironmentWhenNothingIsConfigured(t *testing.T) {
	t.Setenv("PANTALK_PROCENV_TEST", "inherited")

	for name, entries := range map[string]map[string]string{
		"nil":   nil,
		"empty": {},
	} {
		cmd := &exec.Cmd{}
		Apply(cmd, entries)

		if cmd.Env == nil {
			t.Fatalf("%s entries: environment is nil, so exec would inherit the daemon's", name)
		}
		if len(cmd.Env) != 0 {
			t.Fatalf("%s entries: environment = %v, want empty", name, cmd.Env)
		}
	}
}

func TestApplyIgnoresNilCommand(t *testing.T) {
	Apply(nil, map[string]string{"KEY": "value"})
}

func TestInheritCopiesNamedVariables(t *testing.T) {
	t.Setenv("PANTALK_INHERIT_KEEP", "kept")
	t.Setenv("PANTALK_INHERIT_DROP", "dropped")

	inherited := Inherit([]string{"PANTALK_INHERIT_KEEP"})

	if got := inherited["PANTALK_INHERIT_KEEP"]; got != "kept" {
		t.Fatalf("PANTALK_INHERIT_KEEP = %q, want %q", got, "kept")
	}
	if _, ok := inherited["PANTALK_INHERIT_DROP"]; ok {
		t.Fatal("an unnamed variable was inherited")
	}
}

func TestInheritSkipsUnsetNames(t *testing.T) {
	if inherited := Inherit([]string{"PANTALK_INHERIT_ABSENT"}); len(inherited) != 0 {
		t.Fatalf("inherited = %v, want empty for an unset name", inherited)
	}
}

func TestInheritExpandsPrefixWildcards(t *testing.T) {
	t.Setenv("PANTALK_PREFIX_ONE", "1")
	t.Setenv("PANTALK_PREFIX_TWO", "2")
	t.Setenv("PANTALK_OTHER", "3")

	inherited := Inherit([]string{"PANTALK_PREFIX_*"})

	if len(inherited) != 2 {
		t.Fatalf("inherited = %v, want the two PANTALK_PREFIX_ variables", inherited)
	}
	if _, ok := inherited["PANTALK_OTHER"]; ok {
		t.Fatal("a variable outside the prefix was inherited")
	}
}

func TestInheritReturnsNilWithoutNames(t *testing.T) {
	if inherited := Inherit(nil); inherited != nil {
		t.Fatalf("inherited = %v, want nil", inherited)
	}
}
