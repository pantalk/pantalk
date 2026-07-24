package procenv

import (
	"os/exec"
	"slices"
	"testing"
)

func TestApplyAppendsSortedOverridesToExplicitEnvironment(t *testing.T) {
	cmd := &exec.Cmd{Env: []string{"INHERITED=1"}}
	Apply(cmd, map[string]string{
		"B_SECOND": "2",
		"A_FIRST":  "1",
	})

	want := []string{"INHERITED=1", "A_FIRST=1", "B_SECOND=2"}
	if !slices.Equal(cmd.Env, want) {
		t.Fatalf("environment = %v, want %v", cmd.Env, want)
	}
}

func TestApplyInheritsDaemonEnvironmentWhenUnset(t *testing.T) {
	t.Setenv("PANTALK_PROCENV_TEST", "inherited")

	cmd := &exec.Cmd{}
	Apply(cmd, map[string]string{"OVERRIDE": "value"})

	if !slices.Contains(cmd.Env, "PANTALK_PROCENV_TEST=inherited") {
		t.Fatalf("daemon environment was not inherited: %v", cmd.Env)
	}
	if last := cmd.Env[len(cmd.Env)-1]; last != "OVERRIDE=value" {
		t.Fatalf("override is not last (so it may not win): %q", last)
	}
}

func TestApplyLeavesCommandUntouchedWithoutOverrides(t *testing.T) {
	cmd := &exec.Cmd{}
	Apply(cmd, nil)
	if cmd.Env != nil {
		t.Fatalf("environment = %v, want nil so exec inherits", cmd.Env)
	}

	explicit := &exec.Cmd{Env: []string{"ONLY=1"}}
	Apply(explicit, map[string]string{})
	if !slices.Equal(explicit.Env, []string{"ONLY=1"}) {
		t.Fatalf("explicit environment was modified: %v", explicit.Env)
	}
}
