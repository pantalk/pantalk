// Package procenv applies configured environment overrides to the child
// processes Pantalk launches for agents.
package procenv

import (
	"os"
	"os/exec"
	"sort"
)

// Apply appends overrides to a command's environment.
//
// A command that has not set an explicit environment inherits the daemon's,
// matching exec's own default, so agents keep the user's existing CLI
// authentication, settings, and PATH. Overrides are appended last and sorted,
// so they win over inherited values and repeated invocations produce an
// identical environment. With no overrides the command is left untouched and
// plain inheritance remains in effect.
func Apply(cmd *exec.Cmd, overrides map[string]string) {
	if cmd == nil || len(overrides) == 0 {
		return
	}

	base := cmd.Env
	if base == nil {
		base = os.Environ()
	}

	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(base)+len(keys))
	env = append(env, base...)
	for _, key := range keys {
		env = append(env, key+"="+overrides[key])
	}
	cmd.Env = env
}
